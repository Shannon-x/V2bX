package xray

import (
	stdlog "log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/InazumaV/V2bX/conf"
	log "github.com/sirupsen/logrus"
	applog "github.com/xtls/xray-core/app/log"
	"github.com/xtls/xray-core/common"
	clog "github.com/xtls/xray-core/common/log"
	"gopkg.in/natefinch/lumberjack.v2"
)

// With stock xray behavior an empty AccessPath sends every accepted
// connection to stdout, which floods journald on busy nodes (hundreds of MB
// per day) with no retention control. V2bX instead defaults connection logs
// to this rotated file so they stay queryable for a bounded number of days.
// A var (not const) so tests can redirect it.
var defaultAccessLogPath = func() string {
	if runtime.GOOS == "windows" {
		if pd := os.Getenv("ProgramData"); pd != "" {
			return filepath.Join(pd, "V2bX", "access.log")
		}
		return filepath.Join(`C:\`, "V2bX", "access.log")
	}
	return "/var/log/V2bX/access.log"
}()

type logRotateOptions struct {
	maxSizeMB  int
	maxBackups int
	maxDays    int
	compress   bool
}

var (
	rotateMu sync.RWMutex
	// Matches the defaults in conf.NewXrayConfig so rotation is bounded even
	// if applyLogRotateOptions is never reached.
	rotateOpts = logRotateOptions{maxSizeMB: 100, maxBackups: 0, maxDays: 90, compress: true}
)

func applyLogRotateOptions(c *conf.XrayLogConfig) {
	opts := logRotateOptions{
		maxSizeMB:  c.MaxSize,
		maxBackups: c.MaxBackups,
		maxDays:    c.MaxDays,
		compress:   c.Compress,
	}
	if opts.maxSizeMB <= 0 {
		opts.maxSizeMB = 100
	}
	if opts.maxDays < 0 {
		opts.maxDays = 0
	}
	if opts.maxBackups < 0 {
		opts.maxBackups = 0
	}
	rotateMu.Lock()
	rotateOpts = opts
	rotateMu.Unlock()

	rotatorsMu.Lock()
	all := make([]*sharedRotator, 0, len(rotators))
	for _, r := range rotators {
		all = append(all, r)
	}
	rotatorsMu.Unlock()
	for _, r := range all {
		r.reconfigure(opts)
	}
}

func currentRotateOptions() logRotateOptions {
	rotateMu.RLock()
	defer rotateMu.RUnlock()
	return rotateOpts
}

func newLumberjack(path string, o logRotateOptions) *lumberjack.Logger {
	return &lumberjack.Logger{
		Filename:   path,
		MaxSize:    o.maxSizeMB,
		MaxBackups: o.maxBackups,
		MaxAge:     o.maxDays,
		Compress:   o.compress,
		LocalTime:  true,
	}
}

// sharedRotator is the single writer behind one log file path, alive for the
// whole process. xray's generalLogger creates a Writer per activity burst and
// closes it after a minute of idle; constructing a lumberjack.Logger per
// burst would leak its millRun goroutine on every close (lumberjack never
// stops it), so all writers for a path share this one and their Close is a
// no-op.
type sharedRotator struct {
	path string

	mu            sync.Mutex // guards lj swap, writes and forced rotation
	lj            *lumberjack.Logger
	opts          logRotateOptions
	lastWarn      time.Time
	lastPathCheck time.Time

	logger *stdlog.Logger
}

// rotators only grows: entries for paths dropped from the config keep their
// rotateDaily goroutine until process exit. Growth is bounded by the number
// of distinct log paths ever configured, and a stale entry's disk handle is
// released by rotateIfNonEmpty once its file is deleted, so no eviction is
// attempted (safely evicting would require proof no old core still writes).
var (
	rotatorsMu sync.Mutex
	rotators   = make(map[string]*sharedRotator)
)

func getRotator(path string) *sharedRotator {
	rotatorsMu.Lock()
	defer rotatorsMu.Unlock()
	if r, ok := rotators[path]; ok {
		return r
	}
	opts := currentRotateOptions()
	r := &sharedRotator{
		path: path,
		lj:   newLumberjack(path, opts),
		opts: opts,
	}
	// Same prefix flags as xray's own fileLogWriter so log lines keep the
	// "2006/01/02 15:04:05.000000" timestamp format.
	r.logger = stdlog.New(r, "", stdlog.Ldate|stdlog.Ltime|stdlog.Lmicroseconds)
	rotators[path] = r
	go r.rotateDaily()
	return r
}

// Write implements io.Writer for the stdlog logger. Write failures are
// surfaced through logrus (throttled) instead of being swallowed: unlike the
// stock xray file writer, lumberjack only opens the file lazily, so a broken
// path would otherwise drop logs without a trace.
func (r *sharedRotator) Write(p []byte) (int, error) {
	r.mu.Lock()
	if time.Since(r.lastPathCheck) > 30*time.Second {
		r.lastPathCheck = time.Now()
		if _, statErr := os.Stat(r.path); os.IsNotExist(statErr) {
			// The live file was deleted or renamed externally (rm, a
			// non-copytruncate logrotate rule). lumberjack holds the fd and
			// would keep writing the unlinked inode until MaxSize; closing
			// makes this write recreate the path.
			_ = r.lj.Close()
		}
	}
	n, err := r.lj.Write(p)
	warn := false
	if err != nil && time.Since(r.lastWarn) > time.Minute {
		r.lastWarn = time.Now()
		warn = true
	}
	r.mu.Unlock()
	if warn {
		log.WithFields(log.Fields{"path": r.path, "err": err}).
			Warn("Failed to write xray log file, log lines are being dropped")
	}
	return n, err
}

func (r *sharedRotator) reconfigure(opts logRotateOptions) {
	r.mu.Lock()
	if r.opts == opts {
		r.mu.Unlock()
		return
	}
	old := r.lj
	r.lj = newLumberjack(r.path, opts)
	r.opts = opts
	r.mu.Unlock()
	// The old logger's millRun goroutine is orphaned by lumberjack itself
	// (Close never stops it), and if it is gzipping a backup at this exact
	// moment the old and new millers can race on that one file. Both are
	// accepted: this path only runs when rotation settings actually change,
	// which is rare, and the race window is a single in-flight compression.
	old.Close()
}

// rotateDaily force-splits the live file shortly after each local midnight so
// backups line up with calendar days and MaxDays pruning also reaches
// low-traffic nodes whose file would never hit MaxSize.
func (r *sharedRotator) rotateDaily() {
	for {
		time.Sleep(time.Until(nextDailyRotation(time.Now())))
		if err := r.rotateIfNonEmpty(); err != nil {
			log.WithFields(log.Fields{"path": r.path, "err": err}).
				Warn("Daily xray log rotation failed")
		}
	}
}

func nextDailyRotation(now time.Time) time.Time {
	y, m, d := now.Date()
	next := time.Date(y, m, d, 0, 0, 5, 0, now.Location()).AddDate(0, 0, 1)
	// In timezones whose DST spring-forward gap swallows midnight (e.g.
	// America/Santiago), the target can normalize to an instant not after
	// now; without this guard the rotate loop would spin until the clock
	// jumps, force-rotating on every iteration.
	if !next.After(now) {
		next = now.Add(time.Hour)
	}
	return next
}

func (r *sharedRotator) rotateIfNonEmpty() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, err := os.Stat(r.path)
	if os.IsNotExist(err) {
		// Path vanished while idle (deleted externally, or this rotator's
		// path was dropped from the config): release the possibly-unlinked
		// inode so its disk space frees; the next write recreates the file.
		return r.lj.Close()
	}
	if err != nil || st.Size() == 0 {
		// Nothing logged since the last split; skip to avoid empty backups.
		return nil
	}
	return r.lj.Rotate()
}

// newRotateWriter returns an xray log Writer backed by the shared rotator for
// the path. The xray logger creates a writer per activity burst and closes it
// after a minute of idle, so this must be cheap and Close must not tear the
// rotator down.
func newRotateWriter(path string) clog.Writer {
	return &rotateLogWriter{r: getRotator(path)}
}

type rotateLogWriter struct {
	r *sharedRotator
}

func (w *rotateLogWriter) Write(s string) error {
	w.r.logger.Print(s)
	return nil
}

// Close is a no-op: the shared rotator outlives the recreate-per-burst writer
// lifecycle and is reused for the whole process.
func (w *rotateLogWriter) Close() error {
	return nil
}

func init() {
	// Take over xray's file logging so access/error log files rotate instead
	// of growing without bound.
	common.Must(applog.RegisterHandlerCreator(applog.LogType_File,
		func(lt applog.LogType, options applog.HandlerCreatorOptions) (clog.Handler, error) {
			path := options.Path
			return clog.NewLogger(func() clog.Writer {
				return newRotateWriter(path)
			}), nil
		}))
}

// resolveAccessLogPath maps the user-facing AccessPath setting to what xray's
// LogConfig.Build expects: "" means console there, so the legacy-console and
// default-file cases have to swap.
func resolveAccessLogPath(path string) string {
	switch path {
	case "":
		if err := ensureLogWritable(defaultAccessLogPath); err != nil {
			log.WithFields(log.Fields{"path": defaultAccessLogPath, "err": err}).
				Warn("Access log file not writable, connection logs fall back to console")
			return ""
		}
		return defaultAccessLogPath
	case "console", "stdout":
		return ""
	case "none":
		return "none"
	default:
		if err := ensureLogWritable(path); err != nil {
			log.WithFields(log.Fields{"path": path, "err": err}).
				Warn("Access log file not writable, connection logs may be dropped")
		}
		return path
	}
}

func ensureLogWritable(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	return f.Close()
}

package cmd

import (
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/InazumaV/V2bX/conf"
	vCore "github.com/InazumaV/V2bX/core"
	"github.com/InazumaV/V2bX/limiter"
	"github.com/InazumaV/V2bX/node"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var (
	config string
	watch  bool
)

var serverCommand = cobra.Command{
	Use:   "server",
	Short: "Run V2bX server",
	Run:   serverHandle,
	Args:  cobra.NoArgs,
}

func init() {
	serverCommand.PersistentFlags().
		StringVarP(&config, "config", "c",
			"/etc/V2bX/config.json", "config file path")
	serverCommand.PersistentFlags().
		BoolVarP(&watch, "watch", "w",
			true, "watch file path change")
	command.AddCommand(&serverCommand)
}

func serverHandle(_ *cobra.Command, _ []string) {
	showVersion()
	c := conf.New()
	err := c.LoadFromPath(config)
	if err != nil {
		log.WithField("err", err).Error("Load config file failed")
		return
	}
	switch c.LogConfig.Level {
	case "debug":
		log.SetLevel(log.DebugLevel)
	case "info":
		log.SetLevel(log.InfoLevel)
	case "warn":
		log.SetLevel(log.WarnLevel)
	case "error":
		log.SetLevel(log.ErrorLevel)
	}
	var logFile *os.File
	if c.LogConfig.Output != "" {
		logFile, err = os.OpenFile(c.LogConfig.Output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			log.WithField("err", err).Error("Open log file failed, using stdout instead")
		} else {
			log.SetOutput(logFile)
		}
	}
	// Validate configuration
	if warnings := c.Validate(); len(warnings) > 0 {
		for _, w := range warnings {
			log.WithField("warning", w).Warn("Config validation")
		}
	}
	limiter.Init()
	log.Info("Start V2bX...")
	vc, err := vCore.NewCore(c.CoresConfig)
	if err != nil {
		log.WithField("err", err).Error("new core failed")
		return
	}
	err = vc.Start()
	if err != nil {
		log.WithField("err", err).Error("Start core failed")
		return
	}
	// H-13: guard the shutdown Close against a nil core. A failed reload can
	// leave vc nil (rollback also failed); the deferred close must not panic.
	// Also closes the CURRENT vc (a plain `defer vc.Close()` would capture the
	// startup core and, after reloads reassigned vc, close the wrong one).
	defer func() {
		if vc != nil {
			_ = vc.Close()
		}
	}()
	log.Info("Core ", vc.Type(), " started")
	nodes := node.New()
	err = nodes.Start(c.NodeConfig, vc)
	if err != nil {
		log.WithField("err", err).Error("Run nodes failed")
		return
	}
	log.Info("Nodes started")
	xdns := os.Getenv("XRAY_DNS_PATH")
	sdns := os.Getenv("SING_DNS_PATH")
	if watch {
		// Snapshot the config the running stack was built from so a failed
		// reload can roll back to it. The watcher replaces c.CoresConfig /
		// c.NodeConfig with new slice references before calling the callback,
		// so these references keep pointing at the last-good config.
		runningCores := c.CoresConfig
		runningNodes := c.NodeConfig

		// bringUp builds a fresh core from cores, starts it, and starts the
		// shared nodes manager on it. On any failure it tears down whatever it
		// started and returns (nil, err).
		bringUp := func(cores []conf.CoreConfig, nodeCfg []conf.NodeConfig) (vCore.Core, error) {
			nc, berr := vCore.NewCore(cores)
			if berr != nil {
				return nil, berr
			}
			if berr = nc.Start(); berr != nil {
				return nil, berr
			}
			if berr = nodes.Start(nodeCfg, nc); berr != nil {
				_ = nc.Close()
				return nil, berr
			}
			return nc, nil
		}

		rollback := func() {
			rvc, rerr := bringUp(runningCores, runningNodes)
			if rerr != nil {
				log.WithField("err", rerr).Error("Reload rollback FAILED — proxy service is DOWN, manual restart required")
				vc = nil
				return
			}
			vc = rvc
			log.Warn("Reload failed but rolled back to the last-good config; service restored")
		}

		err = c.Watch(config, xdns, sdns, func() {
			// H-13: transactional reload. The watcher already parsed the new
			// config file (a JSON error aborts before this runs, leaving the
			// service untouched). Build the new core BEFORE tearing anything
			// down: NewCore parses the whole config (routes/outbounds/policy/
			// DNS) but binds no ports, so a bad config (typo, bad cert path,
			// bad cipher, invalid route) is caught here while the current
			// service keeps serving.
			newCores := c.CoresConfig
			newNodes := c.NodeConfig
			newVc, berr := vCore.NewCore(newCores)
			if berr != nil {
				log.WithField("err", berr).Error("Reload aborted: new config failed to build; current service kept running")
				return
			}
			// Port reuse forces close-old-before-start-new.
			nodes.Close()
			if vc != nil {
				if cerr := vc.Close(); cerr != nil {
					log.WithField("err", cerr).Warn("Reload: closing old core reported error")
				}
				vc = nil
			}
			if serr := newVc.Start(); serr != nil {
				log.WithField("err", serr).Error("Reload: start new core failed, rolling back to last-good")
				_ = newVc.Close()
				rollback()
				return
			}
			if serr := nodes.Start(newNodes, newVc); serr != nil {
				log.WithField("err", serr).Error("Reload: start new nodes failed, rolling back to last-good")
				_ = newVc.Close()
				rollback()
				return
			}
			// Success — publish the new stack and advance the last-good snapshot.
			vc = newVc
			runningCores = newCores
			runningNodes = newNodes
			log.Info("Core ", vc.Type(), " reloaded, nodes restarted")
			// P-04: removed the per-reload runtime.GC(); a stop-the-world GC on
			// every config edit has no measured justification.
		})
		if err != nil {
			log.WithField("err", err).Error("start watch failed")
			return
		}
	}
	// clear memory
	runtime.GC()
	// wait exit signal
	{
		osSignals := make(chan os.Signal, 1)
		signal.Notify(osSignals, syscall.SIGINT, syscall.SIGTERM)
		<-osSignals
	}
	// graceful shutdown
	log.Info("Shutting down...")
	c.StopWatch()
	nodes.Close()
	if logFile != nil {
		logFile.Close()
	}
}

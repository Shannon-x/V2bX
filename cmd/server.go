package cmd

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/InazumaV/V2bX/conf"
	"github.com/InazumaV/V2bX/limiter"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var (
	config      string
	watch       bool
	pprofListen string
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
	serverCommand.PersistentFlags().StringVar(&pprofListen, "pprof-listen", "", "opt-in diagnostics on a loopback IP, e.g. 127.0.0.1:6060")
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
	defer func() {
		if logFile != nil {
			logFile.Close()
		}
	}()
	// Validate configuration
	if warnings := c.Validate(); len(warnings) > 0 {
		for _, w := range warnings {
			log.WithField("warning", w).Warn("Config validation")
		}
	}
	limiter.Init()
	diagnostics, err := startDiagnostics(pprofListen)
	if err != nil {
		log.WithError(err).Error("Start diagnostics failed")
		return
	}
	if diagnostics != nil {
		defer diagnostics.Close()
	}
	log.Info("Start V2bX...")
	lastGood := snapshotRuntimeConfig(c)
	active, err := newServerRuntime(lastGood)
	if err != nil {
		log.WithError(err).Error("Build core failed")
		return
	}
	if err := active.Start(); err != nil {
		active.Close()
		log.WithError(err).Error("Start nodes failed")
		return
	}
	// StopWatch waits for any in-flight reload before closing the most recent
	// instance. A direct defer active.Close() would capture the initial one.
	defer func() {
		c.StopWatch()
		if active != nil {
			active.Close()
		}
	}()
	log.Info("Nodes started")
	xdns := os.Getenv("XRAY_DNS_PATH")
	sdns := os.Getenv("SING_DNS_PATH")
	if watch {
		err = c.Watch(config, xdns, sdns, func() {
			desired := snapshotRuntimeConfig(c)
			var applied bool
			var reloadErr error
			active, applied, reloadErr = replaceRuntime(active,
				func() error { return validateRuntimeConfig(desired) },
				func() (managedRuntime, error) { return newServerRuntime(desired) },
				func() (managedRuntime, error) { return newServerRuntime(lastGood) })
			if applied {
				lastGood = desired
			}
			if reloadErr != nil {
				log.WithFields(log.Fields{"err": reloadErr, "serving": active != nil, "applied": applied}).Error("Configuration reload encountered an error")
			} else {
				log.Info("Configuration reloaded")
			}
		})
		if err != nil {
			log.WithError(err).Error("Start config watch failed")
			return
		}
	}
	// wait exit signal
	{
		osSignals := make(chan os.Signal, 1)
		signal.Notify(osSignals, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(osSignals)
		<-osSignals
	}
	// graceful shutdown
	log.Info("Shutting down...")
}

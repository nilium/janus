package main

import (
	"flag"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/golang/glog"

	"golang.org/x/net/context"

	"go.spiff.io/dagr/outflux"
)

var (
	SHUTDOWN, die = mksignal()
)

func main() {
	outflux.Log = outflux.Stdlog
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	SHUTDOWN.DelayFunc(time.Second, cancel)

	config := Config{}
	cfgfiles := flag.Args()
	if len(cfgfiles) == 0 {
		cfgfiles = []string{"-"}
	}

	for _, fp := range cfgfiles {
		if fp == "-" {
			glog.Info("Reading config from standard input...")
		}

		if err := parseConfig(&config, fp); err != nil {
			glog.Fatalf("Unable to load config file %s: %v", fp, err)
		}
	}

	go func() {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, os.Interrupt)
		for sig := range signals {
			glog.Info("Received ", sig, " signal: shutting down")
			die()
			cancel()
		}
	}()

	glog.Info("Started")

	maxreqs := outflux.RequestLimit(config.MaxRequests)
	glog.Infof("%#+ v", config)
	gateways := make([]*gateway, len(config.Ports))
	for i, cfg := range config.Ports {
		var err error
		gateways[i], err = newGateway(cfg, maxreqs)
		if err != nil {
			glog.Fatalf("Error configuring %v -> %v gateway: %v", cfg.Listen, cfg.Forward.Host, err)
		}

	}

	var wg sync.WaitGroup
	wg.Add(len(gateways))

	runGateway := func(g *gateway) {
		defer cancel()
		defer wg.Done()
		gwid := g.String()

		glog.Infof("Starting gateway %v", gwid)
		err := g.Start(ctx)
		if err == context.Canceled || err == context.DeadlineExceeded || err == nil {
			glog.Infof("Gateway %v closed", gwid)
			return
		}

		glog.Errorf("Gateway %v failed: %v", gwid, err)
	}

	for _, g := range gateways {
		go runGateway(g)
	}

	go func() {
		select {
		case <-ctx.Done():
		case <-SHUTDOWN:
			return
		}
		glog.Error("Encountered fatal gateway error, shutting down")
		die()
	}()

	<-SHUTDOWN
	wg.Wait()
}

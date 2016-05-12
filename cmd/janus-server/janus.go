package main

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/golang/glog"

	"gopkg.in/yaml.v2"

	"golang.org/x/net/context"

	"go.spiff.io/dagr/outflux"
)

type PortConfig struct {
	Listen         []*Addr       `yaml:"listen"`
	Forward        *URL          `yaml:"dest"`
	FlushInterval  time.Duration `yaml:"flush-every"`
	FlushSizeBytes int           `yaml:"flush-size"` // bytes
	ReadTimeout    time.Duration `yaml:"read-timeout"`
	WriteTimeout   time.Duration `yaml:"write-timeout"`

	MaxRetries int     `yaml:"max-retries"`
	Backoff    backoff `yaml:"backoff"`
}

func (p *PortConfig) UnmarshalYAML(load func(interface{}) error) error {
	type plain PortConfig
	v := plain{
		FlushInterval:  time.Second * 5,
		FlushSizeBytes: 16000,
		ReadTimeout:    time.Second * 10,
		WriteTimeout:   time.Second * 15,

		MaxRetries: 10,
		Backoff:    DefaultBackoff,
	}

	err := load(&v)
	switch {
	case err != nil:
		return err
	case v.Forward == nil:
		return errors.New("ports: porthole missing dest URL")
	case len(v.Listen) == 0:
		return errors.New("ports: porthole missing listen address(es)")
	}

	*p = PortConfig(v)

	return nil
}

func (p *PortConfig) mkproxy(options ...outflux.Option) *outflux.Proxy {
	options = append([]outflux.Option{
		outflux.Timeout(p.WriteTimeout),
		outflux.RetryLimit(p.MaxRetries),
		outflux.FlushSize(p.FlushSizeBytes),
		outflux.BackoffFunc(p.Backoff.backoff),
	}, options...)
	return outflux.NewURL(nil, p.Forward.URL(), options...)
}

type Config struct {
	Ports []*PortConfig `yaml:"ports"`

	MaxRequests int `yaml:"max-requests"`
}

type Signal <-chan struct{}

func (s Signal) Wait()                                { <-s }
func (s Signal) AfterFunc(fn func())                  { go func() { s.Wait(); fn() }() }
func (s Signal) DelayFunc(d time.Duration, fn func()) { go func() { s.Wait(); time.Sleep(d); fn() }() }

type BroadcastFunc func()

func (fn BroadcastFunc) Close() error { fn(); return nil }

func mksignal() (Signal, BroadcastFunc) {
	var (
		sig  = make(chan struct{})
		once sync.Once
	)
	return sig, func() { once.Do(func() { close(sig) }) }
}

var (
	SHUTDOWN, die = mksignal()

	cfgfiles stringvars
)

func init() {
	flag.Var(&cfgfiles, "f", "Load `config file` at startup")
}

func loadyaml(dst interface{}, fpath string) (err error) {
	fi := os.Stdin
	if fpath != "-" {
		fi, err = os.Open(fpath)
		if err != nil {
			return err
		}
		defer fi.Close()
	}

	b, err := ioutil.ReadAll(fi)
	if err != nil {
		return err
	}

	return yaml.Unmarshal(b, dst)
}

func main() {
	outflux.Log = outflux.Stdlog
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	SHUTDOWN.DelayFunc(time.Second, cancel)

	config := Config{}
	if len(cfgfiles) == 0 {
		cfgfiles = []string{"-"}
	}

	for _, fp := range cfgfiles {
		if fp == "-" {
			glog.Info("Reading config from standard input...")
		}

		if err := loadyaml(&config, fp); err != nil {
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

type stringvars []string

func (s *stringvars) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func (s *stringvars) String() string {
	return fmt.Sprint([]string(*s))
}

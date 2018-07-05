package main

import (
	"fmt"

	"go.spiff.io/dagr/outflux"
	"golang.org/x/net/context"
)

type gateway struct {
	cfg *PortConfig
	in  []*porthole
	out *outflux.Proxy
}

func newGateway(cfg *PortConfig, options ...outflux.Option) (g *gateway, err error) {
	proxy := newProxy(cfg, options...)

	var holes []*porthole
	for _, addr := range cfg.Listen {
		var hole *porthole
		hole, err = newPorthole(addr, proxy, cfg.ReadTimeout)

		if err != nil {
			return nil, err
		}
		holes = append(holes, hole)
	}

	{
		dup := new(PortConfig)
		*dup = *cfg
		cfg = dup
	}

	return &gateway{cfg, holes, proxy}, err
}

func (g *gateway) String() string {
	u := *g.cfg.Forward
	u.User = nil
	params := u.Query()
	params.Del("u")
	params.Del("p")
	u.RawQuery = params.Encode()
	return fmt.Sprint(g.cfg.Listen, "->", &u)
}

func (g *gateway) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errch := make(chan error, 3)

	g.out.Start(ctx, g.cfg.FlushInterval)

	for _, p := range g.in {
		go func(p *porthole) {
			err := p.Listen(ctx)
			select {
			case <-ctx.Done():
			case errch <- err:
			}
		}(p)
	}

	go func() { <-ctx.Done(); errch <- ctx.Err() }()

	return <-errch
}

func newProxy(p *PortConfig, options ...outflux.Option) *outflux.Proxy {
	options = append([]outflux.Option{
		outflux.Timeout(p.WriteTimeout),
		outflux.RetryLimit(p.MaxRetries),
		outflux.FlushSize(p.FlushSizeBytes),
		outflux.BackoffFunc(p.Backoff.backoff),
	}, options...)
	return outflux.NewURL(nil, p.Forward, options...)
}

package main

import (
	"errors"
	"net"
	"time"

	"github.com/golang/glog"

	"go.spiff.io/dagr/outflux"
	"golang.org/x/net/context"
)

type porthole struct {
	orig  *Addr
	proxy *outflux.Proxy

	rdtimeout time.Duration
}

func newPorthole(addr *Addr, proxy *outflux.Proxy, rdtimeout time.Duration) (*porthole, error) {
	if addr == nil {
		return nil, errors.New("porthole: addr is nil")
	}

	dup := new(Addr)
	*dup = *addr

	return &porthole{
		orig:      dup,
		rdtimeout: rdtimeout,
		proxy:     proxy,
	}, nil
}

func memclr(block []byte) {
	for i := range block { // optimized to memclr
		block[i] = 0
	}
}

func (p *porthole) listen(ctx context.Context) (err error) {
	if err = ctx.Err(); err != nil {
		return err
	}

	addr, err := p.orig.Resolve()
	if err != nil {
		return err
	}

	conn, err := net.ListenUDP(p.orig.Network, addr)
	if err != nil {
		return err
	}

	// Basically just here to ensure the connection is closed one way or another.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		n      int
		client *net.UDPAddr
		err    error
	}

	var (
		// Sized to ipv4 limit, should be increased or just made to be a configurable buffer
		msg     [65507]byte
		buf     = msg[:]
		n       int
		timeout = p.rdtimeout
		errch   = make(chan result, 1)
	)

	go func() {
		<-ctx.Done()
		errch <- result{err: ctx.Err()}

		clerr := conn.Close()
		if clerr != nil {
			glog.Errorf("Error closing UDP conn for %v: %v", addr, clerr)
		}
	}()

	asyncRead := func(dst []byte) {
		if timeout > 0 {
			if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
				errch <- result{err: err}
				return
			}
		}

		var res result
		res.n, res.client, res.err = conn.ReadFromUDP(dst)
		errch <- res
	}

	read := func(dst []byte) (int, *net.UDPAddr, error) {
		go asyncRead(dst)
		r := <-errch
		return r.n, r.client, r.err
	}

	for {
		n, _, err = read(buf)
		if err == context.Canceled || err == context.DeadlineExceeded {
			// Don't clear buffer or we have a possible race condition, just exit now
			return err
		}

		block := buf[:n]

		switch te := err.(type) {
		case nil:
		case net.Error:
			memclr(block)
			if !te.Temporary() {
				return err
			}
			continue
		default:
			memclr(block)
			return err
		}

		if _, err = p.proxy.Write(block); err != nil {
			return err
		}

		memclr(block)
	}

	if err == nil {
		err = ctx.Err()
	}
	return err
}

func (p *porthole) Listen(ctx context.Context) (err error) {
	const retries = 10
	addr := p.orig.String()
	for i := 1; i <= retries; i++ {
		t := time.Now()
		glog.Infof("[%d] Binding to %v", i, addr)

		err = p.listen(ctx)
		if err == context.Canceled || err == context.DeadlineExceeded || err == nil {
			glog.Infof("[%d] Halting reads on %v", addr)
			return
		}

		if oe, ok := err.(*net.OpError); ok && oe.Op == "listen" {
			// Give up if we failed to even open the listener -- something else is
			// using that port, probably.
			glog.Errorf("[%d] Unable to bind to %v -- will not retry: %v", i, addr, err)
		} else if i == retries {
			glog.Errorf("[%d] All attempts to bind to %v have failed -- will not retry: %v", err)
			return err
		}

		if time.Since(t) > time.Minute {
			// Reset counter if the connection survived long enough -- may be DNS change
			// or server went away mysteriously in this case.
			i = 1
		}

		wait := time.Second*2*time.Duration(i) + DefaultBackoff.backoff(i, retries)
		glog.Errorf("[%d] Unable to bind to %v -- will retry in %v", i, addr, wait)

		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return err
}

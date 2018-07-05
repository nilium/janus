package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"time"

	"go.spiff.io/codf"
)

type Config struct {
	Ports []*PortConfig

	MaxRequests int
}

func parseConfig(dst *Config, fpath string) (err error) {
	doc, err := loadDocument(fpath)
	if err != nil {
		return err
	}
	return codf.Walk(doc, dst)
}

var _ codf.Walker = (*Config)(nil)

func (c *Config) Statement(stmt *codf.Statement) error {
	switch name := stmt.Name(); name {
	case "max-requests":
		return c.handleMaxRequests(stmt.Parameters())
	default:
		return fmt.Errorf("unrecognized directive %s", name)
	}
}

func (c *Config) EnterSection(sect *codf.Section) (codf.Walker, error) {
	switch name := sect.Name(); name {
	case "port":
		return c.enterPort(sect.Parameters())
	default:
		return nil, fmt.Errorf("unrecognized section %s", name)
	}
}

// Config handlers

func (c *Config) handleMaxRequests(args []codf.ExprNode) error {
	return parseArgs(args, &c.MaxRequests)
}

func (c *Config) enterPort(args []codf.ExprNode) (codf.Walker, error) {
	if err := parseArgs(args); err != nil {
		return nil, err
	}
	port := NewPortConfig()
	c.Ports = append(c.Ports, port)
	return port, nil
}

type PortConfig struct {
	Listen         []*Addr
	Forward        *url.URL
	FlushInterval  time.Duration
	FlushSizeBytes int
	WriteTimeout   time.Duration
	ReadTimeout    time.Duration
	MaxRetries     int
	Backoff        backoff
}

func NewPortConfig() *PortConfig {
	return &PortConfig{
		FlushInterval:  time.Second * 5,
		FlushSizeBytes: 16000,
		WriteTimeout:   time.Second * 15,
		ReadTimeout:    time.Second * 10,
		MaxRetries:     10,
		Backoff:        DefaultBackoff,
	}
}

var _ codf.WalkExiter = (*PortConfig)(nil)

func (p *PortConfig) Statement(stmt *codf.Statement) error {
	switch name := stmt.Name(); name {
	case "listen":
		return p.handleListen(stmt.Parameters())
	case "pass":
		return p.handlePass(stmt.Parameters())
	case "flush":
		return p.handleFlush(stmt.Parameters())
	case "max-retries":
		return p.handleMaxRetries(stmt.Parameters())
	case "timeout":
		return p.handleTimeout(stmt.Parameters())
	case "backoff":
		return p.handleBackoff(stmt.Parameters())
	default:
		return fmt.Errorf("unrecognized directive %s", name)
	}
}

func (p *PortConfig) EnterSection(sect *codf.Section) (codf.Walker, error) {
	return nil, fmt.Errorf("unrecognized section %s", sect.Name())
}

func (p *PortConfig) ExitSection(_ codf.Walker, _ *codf.Section, _ codf.ParentNode) error {
	switch {
	case len(p.Listen) == 0:
		return errors.New("port requires at least one listener")
	case p.Forward == nil:
		return errors.New("port requires a forwarding URL")
	}
	return nil
}

func (p *PortConfig) handleListen(args []codf.ExprNode) error {
	for i, arg := range args {
		var s string
		if err := parseArg(arg, &s); err != nil {
			return fmt.Errorf("error parsing parameter %d: %v", i+1, err)
		}
		addr, err := ParseAddr(s)
		if err != nil {
			return fmt.Errorf("error parsing parameter %d: %v", i+1, err)
		}
		p.Listen = append(p.Listen, addr)
	}
	return nil
}

func (p *PortConfig) handlePass(args []codf.ExprNode) error {
	return parseArgs(args, &p.Forward)
}

func (p *PortConfig) handleFlush(args []codf.ExprNode) error {
	if len(args) == 1 {
		return parseArgs(args, &p.FlushInterval)
	}
	return parseArgs(args, &p.FlushInterval, &p.FlushSizeBytes)
}

func (p *PortConfig) handleMaxRetries(args []codf.ExprNode) error {
	return parseArgs(args, &p.MaxRetries)
}

func (p *PortConfig) handleTimeout(args []codf.ExprNode) error {
	if len(args) == 1 {
		var timeout time.Duration
		if err := parseArgs(args, timeout); err != nil {
			return err
		}
		p.ReadTimeout, p.WriteTimeout = timeout, timeout
	}
	return parseArgs(args, &p.WriteTimeout, &p.ReadTimeout)
}

func (p *PortConfig) handleBackoff(args []codf.ExprNode) error {
	if len(args) == 0 {
		return fmt.Errorf("expected 1 or more arguments")
	}

	if err := parseArgsUpTo(args, &p.Backoff.Interval); err != nil {
		return err
	}
	args = args[1:]

	var (
		seenFactor   bool
		seenGrow     bool
		seenMin      bool
		seenMax      bool
		seenMaxExp   bool
		seenExpM     bool
		seenExpScale bool
	)

	b := DefaultBackoff
	for len(args) > 0 {
		// TODO: Better keyword arguments so that errors can be returned for when the
		// keyword matches vs. when the tuple is invalid.
		switch {
		case !seenFactor && parseArgsUpTo(args, Keyword("factor"), &b.Factor) == nil:
			seenFactor, args = true, args[2:]
		case !seenGrow && parseArgsUpTo(args, Keyword("grow-by"), &b.Grow) == nil:
			seenGrow, args = true, args[2:]
		case !seenMin && parseArgsUpTo(args, Keyword("min"), &b.Min) == nil:
			seenMin, args = true, args[2:]
		case !seenMax && parseArgsUpTo(args, Keyword("max"), &b.Max) == nil:
			seenMax, args = true, args[2:]
		case !seenMaxExp && parseArgsUpTo(args, Keyword("exp-max"), &b.MaxExp) == nil:
			seenMaxExp, args = true, args[2:]
		case !seenExpM && parseArgsUpTo(args, Keyword("exp-m"), &b.ExpM) == nil:
			seenExpM, args = true, args[2:]
		case !seenExpScale && parseArgsUpTo(args, Keyword("exp-y"), &b.ExpScale) == nil:
			seenExpScale, args = true, args[2:]
		default:
			return fmt.Errorf("invalid argument %v", args[0].Token().Value)
		}
	}

	if err := b.Check(); err != nil {
		return err
	}

	p.Backoff = b
	return nil
}

type Addr struct {
	Network string
	Addr    string
}

func ParseAddr(hostport string) (addr *Addr, err error) {
	netw := "udp"
	switch u, err := url.Parse(hostport); {
	case err != nil:
	case u.Fragment != "":
		return nil, fmt.Errorf("invalid address: cannot have a fragment")
	case u.RawQuery != "":
		return nil, fmt.Errorf("invalid address: cannot have a query string")
	case u.Path != "":
		return nil, fmt.Errorf("invalid address: cannot have a path")
	case u.Scheme == "":
		return nil, fmt.Errorf("invalid address: must have a protocol")
	case u.Scheme != "" && u.Opaque != "":
		netw = u.Scheme
		hostport = u.Opaque
	case u.Scheme != "" && u.Host != "":
		netw = u.Scheme
		hostport = u.Host
	}
	switch netw {
	case "udp", "udp4", "udp6":
	default:
		return nil, fmt.Errorf("invalid protocol %q; must be udp, udp4, or udp6", netw)
	}
	if _, p, err := net.SplitHostPort(hostport); err != nil {
		return nil, err
	} else if p == "" {
		return nil, fmt.Errorf("no port given")
	}
	return &Addr{
		Network: netw,
		Addr:    hostport,
	}, nil
}

func (a *Addr) String() string { return a.Network + "(" + a.Addr + ")" }

func (a *Addr) Resolve() (*net.UDPAddr, error) {
	return net.ResolveUDPAddr(a.Network, a.Addr)
}

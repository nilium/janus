package main

import (
	"fmt"
	"net"
	"net/url"
)

type URL url.URL

func (u *URL) UnmarshalYAML(load func(interface{}) error) error {
	var str string
	if err := load(&str); err != nil {
		return err
	}

	uri, err := url.Parse(str)
	if err != nil {
		return err
	}

	*u = URL(*uri)
	return nil
}

func (u *URL) MarshalYAML() (interface{}, error) {
	return u.URL().String(), nil
}

func (u *URL) URL() *url.URL {
	return (*url.URL)(u)
}

type Addr struct {
	Network string `yaml:"net"`
	Addr    string `yaml:"addr"`
}

func (a *Addr) UnmarshalYAML(load func(interface{}) error) error {
	type plain Addr
	var v plain

	err := load(&v)
	if err != nil {
		err = load(&v.Addr)
		v.Network = "udp" // Force it
	}

	if err != nil {
		return err
	}

	switch v.Network {
	case "udp", "udp4", "udp6":
	case "":
		v.Network = "udp"
	default:
		return fmt.Errorf("unacceptable network %q: must be udp, udp4, or udp6", v.Network)
	}

	*a = Addr(v)

	return nil
}

func (a *Addr) String() string { return a.Network + "(" + a.Addr + ")" }

func (a *Addr) Resolve() (*net.UDPAddr, error) {
	return net.ResolveUDPAddr(a.Network, a.Addr)
}

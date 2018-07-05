package main

import (
	"crypto/rand"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/golang/glog"
)

type backoff struct {
	Interval time.Duration // Defaults to 15s
	Factor   float64       // Defaults to 1
	Grow     time.Duration // Defaults to 1s
	Min      time.Duration // Defaults to 7s
	Max      time.Duration // Defaults to 3m
	MaxExp   int           // 63 if out of range or 0
	ExpM     float64       // >= 0
	ExpScale float64       // (minimum is >= 0, defaults to 1.5)
}

func (b *backoff) Check() error {
	switch {
	case b.Factor < 1:
		return fmt.Errorf("factor must be > 1; got %v", b.Factor)
	case b.Grow < 0:
		return fmt.Errorf("grow must be >= 0s; got %s", b.Grow)
	case b.Min < 0:
		return fmt.Errorf("min must be >= 0s; got %s", b.Min)
	case b.Max < b.Min:
		return fmt.Errorf("max must be >= min (%s); got %s", b.Min, b.Max)
	case b.MaxExp <= 0 || b.MaxExp > 60:
		return fmt.Errorf("max-exp must be > 0 and <= 60; got %f", b.MaxExp)
	case b.ExpM < 0:
		return fmt.Errorf("exp-m must be >= 0; got %f", b.ExpM)
	case b.ExpScale < 0:
		return fmt.Errorf("exp-y must be >= 0; got %f", b.ExpScale)
	}
	return nil
}

var DefaultBackoff = backoff{15 * time.Second, 1, time.Second, 7 * time.Second, 3 * time.Minute, 20, 1, 1.5}

func (b *backoff) randfactor(retry int) float64 {
	if retry < 1 {
		return 0
	}

	maxex := b.MaxExp
	if maxex <= 0 || maxex > 60 {
		maxex = 60
	}

	if retry > maxex {
		retry = maxex
	}

	max := big.NewInt(128 + 1<<uint(retry))
	max = max.Add(max.Lsh(max, uint(retry-1)), big.NewInt(128))

	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		panic(err)
	}

	f := big.NewFloat(4)
	tf := new(big.Float).SetInt(max)
	tf = tf.Quo(tf, f)
	tf = tf.Add(tf, f.SetFloat64(b.ExpM))
	f = f.Quo(f.SetInt(n), tf)

	if b.ExpScale > 0 {
		f = f.Mul(f, tf.SetFloat64(b.ExpScale))
	}

	r, _ := f.Float64()
	if math.IsInf(r, 0) {
		r = 16000000 // Probably just going to result in hitting max.
	}

	return r
}

func (b *backoff) backoff(retry, _ int) time.Duration {
	if retry < 1 {
		return b.Min
	}

	next := b.Interval
	if factor := (b.Factor * float64(retry)) * float64(b.Grow); factor > 0 {
		next += time.Duration(factor * b.randfactor(retry))
	}

	if next < 0 {
		next = 0
	} else if min := b.Min; min > 0 && next < min {
		next = min
	} else if max := b.Max; max > 0 && next > max {
		next = max
	}

	glog.Info("retry=", retry, " next=", next)
	return next
}

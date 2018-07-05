package main

import (
	"bufio"
	"encoding"
	"fmt"
	"math"
	"math/big"
	"net/url"
	"os"
	"reflect"
	"time"

	"go.spiff.io/codf"
)

func loadDocument(fpath string) (doc *codf.Document, err error) {
	fi := os.Stdin
	if fpath != "-" {
		fi, err = os.Open(fpath)
		if err != nil {
			return nil, err
		}
		defer fi.Close()
	}

	r := bufio.NewReader(fi)
	lex := codf.NewLexer(r)
	parser := codf.NewParser()
	if err := parser.Parse(lex); err != nil {
		return nil, err
	}
	return parser.Document(), nil
}

func parseArgsUpTo(args []codf.ExprNode, dest ...interface{}) error {
	if len(args) > len(dest) {
		args = args[:len(dest)]
	}
	return parseArgs(args, dest...)
}

func parseArgs(args []codf.ExprNode, dest ...interface{}) error {
	if len(args) != len(dest) {
		return fmt.Errorf("expected %d arguments; got %d", len(dest), len(args))
	}
	for i, p := range dest {
		if err := parseArg(args[i], p); err != nil {
			return fmt.Errorf("error parsing parameter %d: %v", i+1, err)
		}
	}
	return nil
}

func parseArg(arg codf.ExprNode, dest interface{}) error {
	const maxUint = ^uint(0)
	const minUint = 0
	const maxInt = int(maxUint >> 1)
	const minInt = -maxInt - 1

	var expected string

	switch v := dest.(type) {
	case Keyword:
		if w, ok := codf.Word(arg); ok && w == string(v) {
			return nil
		}
		expected = "keyword " + string(v)

	case **big.Int:
		if bi := codf.BigInt(arg); bi != nil {
			*v = new(big.Int).Set(bi)
			return nil
		}
		expected = "bigint"

	case *big.Int:
		if bi := codf.BigInt(arg); bi == nil {
			v.Set(bi)
			return nil
		}
		expected = "bigint"

	case *float64:
		if f, ok := codf.Float64(arg); ok {
			*v = f
			return nil
		}
		expected = "float"

	case *float32:
		if f, ok := codf.Float64(arg); ok {
			*v = float32(f)
			return nil
		}
		expected = "float"

	case *int:
		if i, ok := codf.Int64(arg); ok {
			if maxInt != math.MaxInt64 && i > int64(maxInt) || i < int64(minInt) {
				return fmt.Errorf("integer out of range: must be within %d..%d",
					minInt, maxInt)
			}
			*v = int(i)
			return nil
		}
		expected = "integer"

	case *int64:
		if i, ok := codf.Int64(arg); ok {
			*v = i
			return nil
		} else if bi := codf.BigInt(arg); bi != nil && !bi.IsInt64() {
			return fmt.Errorf("integer out of range: must be within %d..%d",
				math.MinInt64, math.MaxInt64)
		}
		expected = "integer"

	case *int32:
		if i, ok := codf.Int64(arg); ok {
			if i > math.MaxInt32 || i < math.MinInt32 {
				return fmt.Errorf("integer out of range: must be within %d..%d",
					math.MinInt32, math.MaxInt32)
			}
			*v = int32(i)
			return nil
		}
		expected = "integer"

	case *int16:
		if i, ok := codf.Int64(arg); ok {
			if i > math.MaxInt16 || i < math.MinInt16 {
				return fmt.Errorf("integer out of range: must be within %d..%d",
					math.MinInt16, math.MaxInt16)
			}
			*v = int16(i)
			return nil
		}
		expected = "integer"

	case *string:
		if s, ok := codf.String(arg); ok {
			*v = s
			return nil
		}
		expected = "string"

	case *Quote:
		if s, ok := codf.Quote(arg); ok {
			*v = Quote(s)
			return nil
		}
		expected = "quoted string"

	case *Word:
		if s, ok := codf.Word(arg); ok {
			*v = Word(s)
			return nil
		}
		expected = "quoted string"

	case *url.URL:
		if s, ok := codf.String(arg); ok {
			u, err := url.Parse(s)
			if err != nil {
				return err
			}
			*v = *u
		}
		expected = "URL"

	case **url.URL:
		if s, ok := codf.String(arg); ok {
			u, err := url.Parse(s)
			if err != nil {
				return err
			}
			*v = u
			return nil
		}
		expected = "URL"

	case *time.Duration:
		if d, ok := codf.Duration(arg); ok {
			*v = d
			return nil
		}
		expected = "duration"

	case encoding.TextUnmarshaler:
		if s, ok := codf.String(arg); ok {
			return v.UnmarshalText([]byte(s))
		}
		expected = reflect.TypeOf(v).Name()

	default:
		return fmt.Errorf("cannot parse argument of type %T", dest)
	}
	return fmt.Errorf("expected %s; got %s",
		expected, arg.Token().Kind)
}

// Auxiliary types for performing more specific matches

type Keyword string

type Word string

func word(s *string) *Word {
	return (*Word)(s)
}

type Quote string

func quote(s *string) *Quote {
	return (*Quote)(s)
}

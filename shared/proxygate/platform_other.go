//go:build !linux

package proxygate

import "context"

func LoadPolicy(string) (*Policy, error) { return nil, ErrUnsupported }
func ListenControl(context.Context, string, *Gate) (*ControlServer, error) {
	return nil, ErrUnsupported
}
func DialControl(context.Context, string, Policy) (*Controller, error) { return nil, ErrUnsupported }

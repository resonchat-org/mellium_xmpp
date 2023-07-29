// Copyright 2023 The Mellium Contributors.
// Use of this source code is governed by the BSD 2-clause
// license that can be found in the LICENSE file.

// Package openfire facilitates integration testing against Openfire.
//
// Unlike all the other software we test against, Openfire is not necessarily a
// single script or executable that can be found by scanning PATH.
// It's a collection of jar files somewhere on the filesystem.
// Some distros may have a script called "openfire" or similar (ie. Arch) but
// others may expect you to invoke Java directly or use a specific init system
// (eg. Alpine).
// To work around this we use a few environment variables to allow overriding
// the automatically detected configuration.
//
//   - OPENFIRE_CMD — the path to the executable to run eg. "/usr/bin/java" or
//     "openfire", defaults to "java". If using a script, make sure you can
//     still pass Java args)
//   - OPENFIRE_ARGS — arguments to pass to the command, eg.
//     "-jar=/var/lib/openfire/lib/startup.jar"
//
// Eg. to launch Openfire manually you might set OPENFIRE_CMD=/usr/bin/java and
// then set OPENFIRE_ARGS=-jar /var/lib/openfire/lib/startup.jar…
package openfire // import "mellium.im/xmpp/internal/integration/openfire"

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mellium.im/xmpp/internal/integration"
	"mellium.im/xmpp/jid"
)

const (
	cfgFileName = "openfire.xml"
	logCfgName  = "log4j2.xml"
)

var cmdName = "java"

// For more information on the environment variables see the package
// documentation at the top of this file.
func init() {
	if envCmd := os.Getenv("OPENFIRE_CMD"); envCmd != "" {
		cmdName = envCmd
	}
}

// New creates a new, unstarted, Openfire daemon.
//
// The provided context is used to kill the process (by calling os.Process.Kill)
// if the context becomes done before the command completes on its own.
func New(ctx context.Context, opts ...integration.Option) (*integration.Cmd, error) {
	return integration.New(
		ctx, cmdName,
		opts...,
	)
}

// ConfigFile is an option that can be used to write a temporary Openfire config
// file.
// This will overwrite the existing config file and make most of the other
// options in this package noops.
// This option only exists for the rare occasion that you need complete control
// over the config file.
func ConfigFile(cfg Config) integration.Option {
	return func(cmd *integration.Cmd) error {
		cmd.Config = cfg
		return integration.TempFile(cfgFileName, func(cmd *integration.Cmd, w io.Writer) error {
			return cfgTmpl.Execute(w, struct {
				Config
				ConfigDir string
			}{
				Config:    cfg,
				ConfigDir: cmd.ConfigDir(),
			})
		})(cmd)
	}
}

func getConfig(cmd *integration.Cmd) Config {
	if cmd.Config == nil {
		cmd.Config = Config{}
	}
	return cmd.Config.(Config)
}

// ListenC2S listens for client-to-server (c2s) connections on a random port.
func ListenC2S() integration.Option {
	return func(cmd *integration.Cmd) error {
		c2sListener, err := cmd.C2SListen("tcp4", "127.0.0.1:0")
		if err != nil {
			return err
		}
		// Openfire creates its own sockets and doesn't provide us with a way of
		// pointing it at an existing Unix domain socket or handing the filehandle
		// for the TCP connection to it on start, so we're effectively just
		// listening to get a random port that we'll use to configure Openfire, then
		// we need to close the connection and let Openfire listen on that port.
		// Technically this is racey, but it's not likely to be a problem in
		// practice.
		c2sPort := c2sListener.Addr().(*net.TCPAddr).Port
		err = c2sListener.Close()
		if err != nil {
			return err
		}

		cfg := getConfig(cmd)
		cfg.C2SPort = c2sPort
		cmd.Config = cfg
		return nil
	}
}

// VHost configures one or more virtual hosts.
// The default if this option is not provided is to create a single vhost called
// "localhost" and create a self-signed cert for it (if VHost is specified certs
// must be manually created).
func VHost(hosts ...string) integration.Option {
	return func(cmd *integration.Cmd) error {
		cfg := getConfig(cmd)
		cfg.VHosts = append(cfg.VHosts, hosts...)
		cmd.Config = cfg
		return nil
	}
}

// CreateUser returns an option that configures Openfire to provision a user
// when starting up.
func CreateUser(ctx context.Context, addr, pass string) integration.Option {
	return func(cmd *integration.Cmd) error {
		j, err := jid.Parse(addr)
		if err != nil {
			return err
		}
		cfg := getConfig(cmd)
		cfg.Users = append(cfg.Users, User{
			Username: j.Bare().String(),
			Password: pass,
		})
		cmd.Config = cfg
		return integration.User(j, pass)(cmd)
	}
}

func defaultConfig(cmd *integration.Cmd) error {
	cfg := getConfig(cmd)
	if len(cfg.VHosts) == 0 {
		const vhost = "localhost"
		cfg.VHosts = append(cfg.VHosts, vhost)
		err := integration.Cert(vhost)(cmd)
		if err != nil {
			return err
		}
	}
	cmd.Config = cfg
	if j, _ := cmd.User(); j.Equal(jid.JID{}) {
		err := CreateUser(context.TODO(), "me@"+cfg.VHosts[0], "password")(cmd)
		if err != nil {
			return err
		}
	}

	err := ConfigFile(cfg)(cmd)
	if err != nil {
		return err
	}
	err = integration.TempFile(logCfgName, func(cmd *integration.Cmd, w io.Writer) error {
		_, err := w.Write(logCfg)
		return err
	})(cmd)
	if err != nil {
		return err
	}
	// For more information on the environment variables see the package
	// documentation at the top of this file.
	const (
		homeFlag = "openfireHome"
		cfgFlag  = "log4j.configurationFile"
	)
	openfireArgs := os.Getenv("OPENFIRE_ARGS")
	args := strings.Fields(openfireArgs)
	if !strings.Contains(openfireArgs, homeFlag) {
		args = append(args, "-D"+homeFlag+"="+cmd.ConfigDir())
	}
	if !strings.Contains(openfireArgs, cfgFlag) {
		args = append(args, "-D"+cfgFlag+"="+filepath.Join(cmd.ConfigDir(), logCfgName))
	}
	return integration.Args(args...)(cmd)
}

// Test starts an Openfire instance and returns a function that runs subtests
// using t.Run.
// Multiple calls to the returned function will result in uniquely named
// subtests.
// When all subtests have completed, the daemon is stopped.
func Test(ctx context.Context, t *testing.T, opts ...integration.Option) integration.SubtestRunner {
	opts = append(opts, defaultConfig)
	return integration.Test(ctx, cmdName, t, opts...)
}

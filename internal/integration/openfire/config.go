// Copyright 2023 The Mellium Contributors.
// Use of this source code is governed by the BSD 2-clause
// license that can be found in the LICENSE file.

package openfire

import (
	_ "embed"
	"path/filepath"
	"text/template"
)

// Config contains options that can be written to an Openfire config file.
type Config struct {
	C2SPort int
	Admins  []string
	VHosts  []string
	Users   []User
}

// User contains configuration for a new user.
type User struct {
	Username string
	Password string
}

var (
	//go:embed openfire.xml.tmpl
	cfgBase string

	//go:embed log4j2.xml
	logCfg []byte

	cfgTmpl = template.Must(template.New("cfg").Funcs(template.FuncMap{
		"filepathJoin": filepath.Join,
	}).Parse(cfgBase))
)

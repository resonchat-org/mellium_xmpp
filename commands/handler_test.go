// Copyright 2026 The Mellium Contributors.
// Use of this source code is governed by the BSD 2-clause
// license that can be found in the LICENSE file.

package commands_test

import (
	"context"
	"encoding/xml"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"mellium.im/xmlstream"

	"mellium.im/xmpp/commands"
	"mellium.im/xmpp/disco"
	"mellium.im/xmpp/form"
	"mellium.im/xmpp/internal/xmpptest"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/mux"
	"mellium.im/xmpp/stanza"
)

type config struct {
	stage   uint
	lang    string
	service string
	t       *testing.T

	runlevel *string
	state    *string
}

func (config) Node() string {
	return "config"
}

func (config) Name() string {
	return "Configure Service"
}

func (c config) Actions() (actions commands.Actions) {
	switch c.stage {
	case 1:
		actions |= commands.Next | (commands.Next << 3)
	case 2:
		actions |= commands.Prev | commands.Complete | (commands.Complete << 3)
	}
	return
}

func (c config) Execute(lang string) (commands.Stage, xml.TokenReader, []commands.Note, error) {
	if lang != "" && strings.ToLower(lang) != "en-us" {
		return nil, nil, nil, commands.BadLocale
	}
	c.lang = lang
	c.stage = 1
	list := []form.Option{form.Label("Service"), form.ListItem("", "httpd"), form.ListItem("", "jabberd"), form.ListItem("", "postgresql")}
	if c.service != "" {
		list = append(list, form.Value(c.service))
	}
	return c, form.New(
		form.Title(c.Name()),
		form.Instructions("Please select the service to configure."),
		form.List("service", list...),
	).TokenReader(), nil, nil
}

func (c config) Process(action commands.Actions, payload xml.TokenReader) (commands.Stage, xml.TokenReader, []commands.Note, error) {
	switch c.stage {
	case 1:
		if action != commands.Next {
			return nil, nil, nil, commands.BadAction
		}
		var data form.Data
		if err := xml.NewTokenDecoder(payload).Decode(&data); err != nil {
			return nil, nil, nil, commands.BadPayload
		}
		service, ok := data.GetString("service")
		if !ok {
			return nil, nil, nil, commands.BadPayload
		}
		c.service = service
		c.stage = 2
		return c, form.New(
			form.Title(c.Name()),
			form.Instructions(fmt.Sprintf("Please select the run modes and state for '%s'.", c.service)),
			form.ListMulti(
				"runlevel",
				form.Label("Run Modes"),
				form.Value("3"),
				form.Value("5"),
				form.ListItem("Single-User", "1"),
				form.ListItem("Non-Networked Multi-User", "2"),
				form.ListItem("Full Multi-User", "3"),
				form.ListItem("X-Window", "5"),
			),
			form.List("state", form.Label("Run State"), form.Value("off"), form.ListItem("Active", "off"), form.ListItem("Inactive", "on")),
		).TokenReader(), nil, nil
	case 2:
		switch action {
		default:
			return nil, nil, nil, commands.BadAction
		case commands.Prev:
			c.stage = 0
			return c.Execute(c.lang)
		case commands.Complete:
			var data form.Data
			if err := xml.NewTokenDecoder(payload).Decode(&data); err != nil {
				return nil, nil, nil, commands.BadPayload
			}
			runlevel, ok := data.GetStrings("runlevel")
			if !ok || len(runlevel) != 1 {
				return nil, nil, nil, commands.BadPayload
			}
			*c.runlevel = runlevel[0]
			state, ok := data.GetString("state")
			if !ok {
				return nil, nil, nil, commands.BadPayload
			}
			*c.state = state
			return nil, nil, []commands.Note{{
				Value: fmt.Sprintf("Service '%s' has been configured", c.service),
			}}, nil
		}
	}
	c.t.Error("reached unexpected stage")
	return nil, nil, nil, fmt.Errorf("reached unexpected stage")
}

func (c config) Cancel() (xml.TokenReader, []commands.Note, error) {
	return nil, nil, nil
}

func TestHandler(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var runlevel, state string

	m := mux.New(stanza.NSClient, disco.Handle(), commands.Handle(jid.MustParse("server"), config{t: t, runlevel: &runlevel, state: &state}))
	s := xmpptest.NewClientServer(
		xmpptest.ServerHandler(m),
		xmpptest.ServerJID(jid.MustParse("server")),
	)
	defer s.Close()

	iter := commands.Fetch(ctx, s.Client.RemoteAddr(), s.Client)
	var cmd commands.Command
	for iter.Next() {
		if command := iter.Command(); command.Node == "config" {
			cmd = command
			break
		}
	}
	if err := iter.Err(); err != nil {
		t.Fatalf("command iteration failed: %v", err)
	}
	if err := iter.Close(); err != nil {
		t.Fatalf("failed to close the iter: %v", err)
	}
	if cmd.Node != "config" {
		t.Fatal("failed to find the command")
	}

	parsePayload := func(payload xml.TokenReader) (form.Data, []commands.Note, commands.Actions, error) {
		var tmp struct {
			Form    form.Data        `xml:"jabber:x:data x"`
			Notes   []commands.Note  `xml:"http://jabber.org/protocol/commands note"`
			Actions commands.Actions `xml:"http://jabber.org/protocol/commands actions"`
		}
		if err := xml.NewTokenDecoder(xmlstream.Wrap(payload, xml.StartElement{Name: xml.Name{Local: "tmp"}})).Decode(&tmp); err != nil {
			return form.Data{}, nil, 0, err
		}
		return tmp.Form, tmp.Notes, tmp.Actions, nil
	}

	var step uint
	if err := cmd.ForEach(ctx, nil, s.Client, func(r commands.Response, payload xml.TokenReader) (commands.Command, xml.TokenReader, error) {
		step = step + 1
		switch step {
		case 3:
			fallthrough
		case 1:
			data, _, actions, err := parsePayload(payload)
			if err != nil {
				return commands.Command{}, nil, fmt.Errorf("failed to parse payload: %w", err)
			}
			if actions&commands.Next == 0 {
				return commands.Command{}, nil, fmt.Errorf("next not allowed: %s", actions)
			}
			services, ok := data.GetOptions("service")
			if !ok {
				return commands.Command{}, nil, fmt.Errorf("no services")
			}
			if step == 3 {
				service, ok := data.GetString("service")
				if !ok || service != "httpd" {
					return commands.Command{}, nil, fmt.Errorf("expected default value to be httpd")
				}
			}
			if !slices.Contains(services, form.FieldOpt{Value: "httpd"}) {
				return commands.Command{}, nil, fmt.Errorf("services don't contain httpd")
			}
			if _, err := data.Set("service", "httpd"); err != nil {
				return commands.Command{}, nil, err
			}
			ret, ok := data.Submit()
			if !ok {
				return commands.Command{}, nil, fmt.Errorf("submission is not OK")
			}
			return r.Next(), ret, nil
		case 2:
			_, _, actions, err := parsePayload(payload)
			if err != nil {
				return commands.Command{}, nil, fmt.Errorf("failed to parse payload: %w", err)
			}
			if actions&commands.Prev == 0 {
				return commands.Command{}, nil, fmt.Errorf("prev not allowed: %s", actions)
			}
			return r.Prev(), nil, nil
		case 4:
			data, _, actions, err := parsePayload(payload)
			if err != nil {
				return commands.Command{}, nil, fmt.Errorf("failed to parse payload: %w", err)
			}
			if actions&commands.Complete == 0 {
				return commands.Command{}, nil, fmt.Errorf("complete not allowed: %s", actions)
			}
			runlevels, ok := data.GetStrings("runlevel")
			if !ok {
				return commands.Command{}, nil, fmt.Errorf("default runlevels not set")
			}
			if len(runlevels) != 2 || runlevels[0] != "3" || runlevels[1] != "5" {
				return commands.Command{}, nil, fmt.Errorf("unexpected default runlevels: %v", runlevels)
			}
			state, ok := data.GetString("state")
			if !ok || state != "off" {
				return commands.Command{}, nil, fmt.Errorf("default state not set or unexpected: %q", state)
			}
			if _, err := data.Set("state", "on"); err != nil {
				return commands.Command{}, nil, err
			}
			if _, err := data.Set("runlevel", []string{"3"}); err != nil {
				return commands.Command{}, nil, err
			}
			ret, ok := data.Submit()
			if !ok {
				return commands.Command{}, nil, fmt.Errorf("submission is not OK")
			}
			return r.Complete(), ret, nil
		case 5:
			if r.Status != "completed" {
				return commands.Command{}, nil, fmt.Errorf("expected completion, got %q", r.Status)
			}
			_, notes, _, err := parsePayload(payload)
			if err != nil {
				return commands.Command{}, nil, fmt.Errorf("failed to parse payload: %w", err)
			}
			if len(notes) != 1 || notes[0].Type != commands.NoteInfo {
				return commands.Command{}, nil, fmt.Errorf("expected exactly one info note, got %v", notes)
			}
			return commands.Command{}, nil, nil
		}
		return commands.Command{}, nil, fmt.Errorf("reached unexpected step")
	}); err != nil {
		t.Fatalf("failed to execute the chain: %v", err)
	}

	if runlevel != "3" || state != "on" {
		t.Fatalf("unexpected runlevel or state: %q and %q", runlevel, state)
	}
}

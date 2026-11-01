// Copyright 2026 The Mellium Contributors / ResonChat contributors.
// Use of this source code is governed by the BSD 2-clause
// license that can be found in the LICENSE file.

package sm_test

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"

	"mellium.im/xmlstream"
	"mellium.im/xmpp"
	"mellium.im/xmpp/sm"
)

func TestWrapCountsStanzas(t *testing.T) {
	h := &sm.Handler{}
	called := false
	next := xmpp.HandlerFunc(func(t xmlstream.TokenReadEncoder, start *xml.StartElement) error {
		called = true
		return nil
	})
	w := h.Wrap(next)
	start := xml.StartElement{Name: xml.Name{Local: "message", Space: "jabber:client"}}
	if err := w.HandleXMPP(&fakeEnc{}, &start); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("inner handler not called")
	}
	if h.HandledCount() != 1 {
		t.Fatalf("handled=%d want 1", h.HandledCount())
	}
}

func TestAckRequestXML(t *testing.T) {
	h := &sm.Handler{}
	h.MarkHandled()
	h.MarkHandled()
	var buf strings.Builder
	enc := &fakeEnc{w: &buf} // empty body → immediate EOF on read
	start := xml.StartElement{Name: xml.Name{Space: sm.NS, Local: "r"}}
	if err := h.Wrap(xmpp.HandlerFunc(func(xmlstream.TokenReadEncoder, *xml.StartElement) error {
		t.Fatal("should not reach next for <r/>")
		return nil
	})).HandleXMPP(enc, &start); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `h="2"`) {
		t.Fatalf("unexpected ack xml: %q", out)
	}
}

type fakeEnc struct {
	w *strings.Builder
}

func (f *fakeEnc) Token() (xml.Token, error) { return nil, io.EOF }

func (f *fakeEnc) EncodeToken(t xml.Token) error {
	if f.w == nil {
		return nil
	}
	switch tt := t.(type) {
	case xml.StartElement:
		f.w.WriteByte('<')
		f.w.WriteString(tt.Name.Local)
		for _, a := range tt.Attr {
			f.w.WriteByte(' ')
			f.w.WriteString(a.Name.Local)
			f.w.WriteString(`="`)
			f.w.WriteString(a.Value)
			f.w.WriteByte('"')
		}
		f.w.WriteByte('>')
	case xml.EndElement:
		f.w.WriteString("</")
		f.w.WriteString(tt.Name.Local)
		f.w.WriteByte('>')
	}
	return nil
}

func (f *fakeEnc) Encode(v interface{}) error { return nil }
func (f *fakeEnc) EncodeElement(v interface{}, start xml.StartElement) error {
	return nil
}
func (f *fakeEnc) Flush() error { return nil }

// Copyright 2026 The Mellium Contributors.
// Use of this source code is governed by the BSD 2-clause
// license that can be found in the LICENSE file.

package xmpptest

import (
	"encoding/xml"

	"mellium.im/xmlstream"

	"mellium.im/xmpp"
)

// NotifyingHandler transparently passes everything to the inner handler,
// sending to the provided channel after every such call.
type NotifyingHandler struct {
	C     chan struct{}
	Inner xmpp.Handler
}

// HandleXMPP implements [mellium.im/xmpp.Handler].
func (h NotifyingHandler) HandleXMPP(t xmlstream.TokenReadEncoder, start *xml.StartElement) error {
	err := h.Inner.HandleXMPP(t, start)
	h.C <- struct{}{}
	return err
}

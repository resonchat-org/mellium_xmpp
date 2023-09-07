// Copyright 2023 The Mellium Contributors.
// Use of this source code is governed by the BSD 2-clause
// license that can be found in the LICENSE file.

package sce

import (
	"crypto/rand"
	"encoding/xml"
	"errors"
	"io"
	"math/big"
	"time"

	"mellium.im/xmlstream"
	"mellium.im/xmpp/internal/attr"
	"mellium.im/xmpp/jid"
)

// ProfileFlags one flag for each potential element of an XEP-0420 envelope
type ProfileFlags struct {
	Rpad, Time, To, From bool
}

// Profile declares an SCE profile with its rules what "SHOULD" and "MUST" be fulfilled
type Profile struct {
	Should, Must ProfileFlags
}

// Envelope packs together all optional elements of an XEP-0420 envelope
type Envelope struct {
	Profile Profile
	Time    time.Time `xml:"time"`
	To      jid.JID   `xml:"to"`
	From    jid.JID   `xml:"from"`
	// Content is expected to be a xml.TokenReader on Marshaling
	//  and will be a string when Unmarshaling
	Content any `xml:"content"`
}

// A list of potential errors returned by this package
var (
	// ErrProfileMissingFrom the SCE Profile requires a 'from' element, but none is given
	ErrProfileMissingFrom = errors.New(`SCE Profile requires a 'from' element, but none is given`)
	// ErrProfileMissingRpad the SCE Profile requires a 'rpad' element, but none is given
	ErrProfileMissingRpad = errors.New(`SCE Profile requires a 'rpad' element, but none is given`)
	// ErrProfileMissingTime the SCE Profile requires a 'time' element, but none is given
	ErrProfileMissingTime = errors.New(`SCE Profile requires a 'time' element, but none is given`)
	// ErrProfileMissingTo the SCE Profile requires a 'to' element, but none is given
	ErrProfileMissingTo = errors.New(`SCE Profile requires a 'to' element, but none is given`)
	// ErrNoTokenreader the type must be xml.Tokenreader
	ErrNoTokenreader = errors.New(`type must be xml.Tokenreader`)
)

// MarshalXML satisfies the xml package's Marshaler interface.
func (env Envelope) MarshalXML(e *xml.Encoder, _ xml.StartElement) error {
	_, err := env.WriteXML(e)
	return err
}

// WriteXML satisfies the xmlstream.WriterTo interface.
// It is like MarshalXML except it writes tokens to w.
func (env Envelope) WriteXML(w xmlstream.TokenWriter) (n int, err error) {
	return xmlstream.Copy(w, env.TokenReader())
}

// TokenReader returns a new xml.TokenReader that returns an encoding of
// the Envelope.
func (env Envelope) TokenReader() xml.TokenReader {
	tr, err := env.Wrap(nil)
	if err != nil {
		panic(err)
	}
	return tr
}

// Validate checks whether the Envelope can be converted into a valid XML
// as of the SCE profile given
func (env *Envelope) Validate() error {
	if env.Profile.Must.From && env.From.Bare().String() == "" {
		return ErrProfileMissingFrom
	}
	if env.Profile.Must.To && env.To.Bare().String() == "" {
		return ErrProfileMissingTo
	}
	return nil
}

// Wrap wraps the content in an envelope.
func (env Envelope) Wrap(content xml.TokenReader) (xml.TokenReader, error) {
	switch env.Content.(type) {
	case xml.TokenReader:
		break
	case string:
		return nil, ErrNoTokenreader
	default:
		if env.Content != nil {
			return nil, ErrNoTokenreader
		}
	}

	var payloads []xml.TokenReader
	payloads = append(payloads, xmlstream.Wrap(
		env.Content.(xml.TokenReader),
		xml.StartElement{Name: xml.Name{Local: "content"}},
	))

	if env.Profile.Should.Rpad || env.Profile.Must.Rpad {
		payloads = append(payloads,
			xmlstream.Wrap(
				xmlstream.Token(xml.CharData(attr.RandomLen(intn(200)))),
				xml.StartElement{Name: xml.Name{Local: "rpad"}}))
	}
	if env.Profile.Should.Time || env.Profile.Must.Time {
		var t time.Time
		if env.Time.String() == "" {
			t = time.Now()
		} else {
			t = env.Time
		}
		payloads = append(payloads,
			xmlstream.Wrap(xmlstream.ReaderFunc(func() (xml.Token, error) {
				return xml.CharData(t.UTC().Format(time.RFC3339)), io.EOF
			}), xml.StartElement{Name: xml.Name{Local: "time"}}))
	}
	if (env.Profile.Should.To || env.Profile.Must.To) && env.To.Bare().String() != "" {
		payloads = append(payloads,
			xmlstream.Wrap(xmlstream.ReaderFunc(func() (xml.Token, error) {
				return xml.CharData(env.To.Bare().String()), io.EOF
			}), xml.StartElement{Name: xml.Name{Local: "to"}}))
	}
	if (env.Profile.Should.From || env.Profile.Must.From) && env.From.Bare().String() != "" {
		payloads = append(payloads,
			xmlstream.Wrap(xmlstream.ReaderFunc(func() (xml.Token, error) {
				return xml.CharData(env.From.Bare().String()), io.EOF
			}), xml.StartElement{Name: xml.Name{Local: "from"}}))
	}
	return xmlstream.Wrap(xmlstream.MultiReader(payloads...), xml.StartElement{
		Name: xml.Name{Local: "envelope", Space: "urn:xmpp:sce:1"},
	}), nil
}

// UnmarshalXML implements xml.Unmarshaler and unmarshals the content from an envelope.
func (env *Envelope) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	v := struct {
		XMLName xml.Name `xml:"urn:xmpp:sce:1 envelope"`

		Time    string `xml:"time"`
		Rpad    string `xml:"rpad"`
		To      string `xml:"to"`
		From    string `xml:"from"`
		Content struct {
			Str string `xml:",innerxml"`
		} `xml:"content"`
	}{}
	err := d.DecodeElement(&v, &start)
	if err != nil {
		return err
	}
	// rpad can be ignored, but must be checked whether it exists if required
	if v.Rpad == "" && env.Profile.Must.Rpad {
		return ErrProfileMissingRpad
	}
	if v.Time == "" && env.Profile.Must.Time {
		return ErrProfileMissingTime
	}
	if v.To == "" && env.Profile.Must.To {
		return ErrProfileMissingTo
	}
	if v.From == "" && env.Profile.Must.From {
		return ErrProfileMissingFrom
	}
	if v.Time != "" {
		env.Time, err = time.Parse(time.RFC3339, v.Time)
		if err != nil {
			return err
		}
	}
	if v.To != "" {
		env.To, err = jid.Parse(v.To)
		if err != nil {
			return err
		}
	}
	if v.From != "" {
		env.From, err = jid.Parse(v.From)
		if err != nil {
			return err
		}
	}
	env.Content = v.Content.Str
	return nil
}

// intn returns, as an int, a non-negative random number in the half-open interval [0,n).
// It panics if n <= 0.
func intn(n byte) int {
	upper_b := make([]byte, 1)
	upper_b[0] = n
	upper := new(big.Int)
	upper = upper.SetBytes(upper_b)
	rnd, err := rand.Int(rand.Reader, upper)
	if err != nil {
		panic(err)
	}
	return int(rnd.Int64())
}

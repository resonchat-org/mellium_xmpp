// Copyright 2023 The Mellium Contributors.
// Use of this source code is governed by the BSD 2-clause
// license that can be found in the LICENSE file.

package sce

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"hash"
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
	// ErrNSMismatch the XML Namespace of encrypted element does not match
	ErrNSMismatch = errors.New(`XML Namespace of encrypted element does not match`)
	// ErrPaddingMismatch the padding does not match
	ErrPaddingMismatch = errors.New(`padding does not match`)
	// ErrMACMismatch the calculated MAC does not match
	ErrMACMismatch = errors.New(`calculated MAC does not match`)
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

// AuthedCrypter is an interface for authenticated encryption
type AuthedCrypter interface {
	// Encrypt encrypts the data and stores the accompanying MAC, which can be retrieved with GetMAC
	Encrypt(p []byte) ([]byte, error)
	// GetMAC returns the calculated MAC of the ciphertext
	GetMAC(dst []byte)
	// SetMAC sets the MAC that will be checked before decrypting the ciphertext
	SetMAC(src []byte)
	// Decrypt first compares the ciphertext to the MAC set by SetMAC, then decrypts the data
	// A missing MAC will lead to a failed decryption
	Decrypt(p []byte) ([]byte, error)
}

// CBCHMACAuthedCrypter is a generic CBC crypting struct
type CBCHMACAuthedCrypter struct {
	key  []byte
	iv   []byte
	mac  []byte
	hmac hash.Hash
}

// NewCBCHMACAuthedCrypter returns an instance of the authenticated crypter which uses AES as block cipher,
// CBC as mode of operation of the block cipher and HMAC as authentication algorithm
func NewCBCHMACAuthedCrypter(aesKey, aesIV, macKey []byte, h func() hash.Hash) AuthedCrypter {
	return &CBCHMACAuthedCrypter{key: aesKey, iv: aesIV, hmac: hmac.New(h, macKey)}
}

// GetMAC returns the calculated MAC of the ciphertext
func (c *CBCHMACAuthedCrypter) GetMAC(dst []byte) {
	copy(dst, c.mac)
}

// SetMAC sets the MAC that will be checked before decrypting the ciphertext
func (c *CBCHMACAuthedCrypter) SetMAC(src []byte) {
	c.mac = make([]byte, len(src))
	copy(c.mac, src)
}

// padLen returns the length of the padding
func padLen(n int) int {
	return aes.BlockSize - (n % aes.BlockSize)
}

// paddedLen returns the total length of the padded buffer
func paddedLen(n int) int {
	return n + padLen(n)
}

// pad does the PKCS#7 style padding of src into dst
func pad(dst, src []byte) {
	// early bounds check
	_ = dst[paddedLen(len(src))-1]
	padlen := padLen(len(src))
	padding := bytes.Repeat([]byte{byte(padlen)}, padlen)
	copy(dst, src)
	copy(dst[len(src):], padding)
}

// Encrypt PKCS#7 pads the plaintext, then CBC encrypts it and base64 encodes the ciphertext
func (c *CBCHMACAuthedCrypter) Encrypt(pt []byte) ([]byte, error) {
	ppt := make([]byte, paddedLen(len(pt)))
	pad(ppt, pt)

	block, err := aes.NewCipher(c.key)
	if err != nil {
		panic(err)
	}

	ct := make([]byte, len(ppt))
	cipher.NewCBCEncrypter(block, c.iv).CryptBlocks(ct, ppt)
	clear(ppt)

	alg := c.hmac
	alg.Write(ct)
	c.mac = alg.Sum(nil)

	b64 := make([]byte, base64.StdEncoding.EncodedLen(len(ct)))
	base64.StdEncoding.Encode(b64, ct)
	clear(ct)

	return b64, nil
}

// unpad does the PKCS#7 style unpadding of buf
func unpad(buf *[]byte) error {
	buflen := len(*buf)
	padlen := (*buf)[buflen-1]

	padstart := buflen - int(padlen)
	for i := padstart; i < buflen; i++ {
		if (*buf)[i] != padlen {
			return ErrPaddingMismatch
		}
	}
	(*buf) = (*buf)[0:padstart]
	return nil
}

// Decrypt does a base64 decoding followed by an CBC decryption and PKCS#7 unpadding
func (c *CBCHMACAuthedCrypter) Decrypt(b64 []byte) ([]byte, error) {
	ct := make([]byte, base64.StdEncoding.DecodedLen(len(b64)))
	dl, err := base64.StdEncoding.Decode(ct, b64)
	if err != nil {
		return nil, err
	}
	// the decoded buffer is potentially shorter than the real decoded data,
	// therefore truncate the slice to its real length
	ct = ct[:dl]

	alg := c.hmac
	alg.Write(ct)
	mac := alg.Sum(nil)
	mac = mac[:len(c.mac)]
	if len(c.mac) == 0 || !hmac.Equal(mac, c.mac) {
		return nil, ErrMACMismatch
	}

	block, err := aes.NewCipher(c.key)
	if err != nil {
		panic(err)
	}

	pt := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, c.iv).CryptBlocks(pt, ct)
	clear(ct)

	err = unpad(&pt)
	if err != nil {
		return nil, err
	}
	return pt, nil
}

// EncryptedEnvelope binds together an Envelope, the required XML Namespace, a crypto method and an optional header
type EncryptedEnvelope struct {
	XMLNamespace  xml.Name
	AuthedCrypter AuthedCrypter
	// Header is expected to be a xml.TokenReader on Marshaling
	//  and will be a string when Unmarshaling
	Header any
	Env    Envelope
}

// MarshalXML satisfies the xml package's Marshaler interface.
func (env EncryptedEnvelope) MarshalXML(e *xml.Encoder, _ xml.StartElement) error {
	_, err := env.WriteXML(e)
	return err
}

// WriteXML satisfies the xmlstream.WriterTo interface.
// It is like MarshalXML except it writes tokens to w.
func (env EncryptedEnvelope) WriteXML(w xmlstream.TokenWriter) (n int, err error) {
	return xmlstream.Copy(w, env.TokenReader())
}

// TokenReader returns a new xml.TokenReader that returns a rendered EncryptedEnvelope.
func (env EncryptedEnvelope) TokenReader() xml.TokenReader {
	tr, err := env.Wrap(nil)
	if err != nil {
		panic(err)
	}
	return tr
}

// Wrap marshals and encrypts an envelope.
func (env EncryptedEnvelope) Wrap(content xml.TokenReader) (xml.TokenReader, error) {
	err := env.Env.Validate()
	if err != nil {
		return nil, err
	}

	switch env.Header.(type) {
	case xml.TokenReader:
		break
	case string:
		return nil, ErrNoTokenreader
	default:
		if env.Header != nil {
			return nil, ErrNoTokenreader
		}
	}

	xb, err := xml.Marshal(env.Env)
	if err != nil {
		return nil, err
	}

	ct, err := env.AuthedCrypter.Encrypt(xb)
	if err != nil {
		return nil, err
	}

	var payloads []xml.TokenReader
	if env.Header != nil {
		payloads = append(payloads, env.Header.(xml.TokenReader))
	}
	payloads = append(payloads, xmlstream.Wrap(
		xmlstream.ReaderFunc(func() (xml.Token, error) {
			return xml.CharData(ct), io.EOF
		}),
		xml.StartElement{Name: xml.Name{Local: "payload"}},
	))
	return xmlstream.Wrap(xmlstream.MultiReader(payloads...), xml.StartElement{
		Name: xml.Name{Local: "encrypted", Space: env.XMLNamespace.Space}}), nil
}

// TODO move everything below this to a separate package
const (
	// OMEMO2_MAC_LENGTH describes the default length of the truncated MAC
	OMEMO2_MAC_LENGTH = 16
)

// XSFOMEMO2SceEncryptedEnvelope is declaring the structure of the XML document
type XSFOMEMO2SceEncryptedEnvelope struct {
	Header struct {
		Str string `xml:",innerxml"`
	} `xml:"header"`
	Payload struct {
		Str string `xml:",chardata"`
	} `xml:"payload"`
}

// XSFOMEMO2SceEnvelope can be used to decrypt an SCE Envelope as defined in XEP-0384 omemo:2
type XSFOMEMO2SceEnvelope struct {
	Encrypted XSFOMEMO2SceEncryptedEnvelope
	Envelope  EncryptedEnvelope
}

// UnmarshalXML decrypts and unmarshals an encrypted envelope.
func (sce *XSFOMEMO2SceEnvelope) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {

	if start.Name.Space != sce.Envelope.XMLNamespace.Space {
		return ErrNSMismatch
	}

	err := d.DecodeElement(&sce.Encrypted, &start)
	if err != nil {
		return err
	}

	pt, err := sce.Envelope.AuthedCrypter.Decrypt([]byte(sce.Encrypted.Payload.Str))
	if err != nil {
		return err
	}

	err = xml.Unmarshal(pt, &sce.Envelope.Env)
	if err != nil {
		return err
	}

	sce.Envelope.Header = sce.Encrypted.Header.Str

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

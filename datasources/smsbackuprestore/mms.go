package smsbackuprestore

import (
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mholt/timeliner"
)

// MMS represents a multimedia message.
type MMS struct {
	CommonSMSandMMSFields
	Rr         string    `xml:"rr,attr"`
	Sub        string    `xml:"sub,attr"`
	CtT        string    `xml:"ct_t,attr"`
	ReadStatus string    `xml:"read_status,attr"`
	Seen       string    `xml:"seen,attr"`
	MsgBox     string    `xml:"msg_box,attr"`
	SubCs      string    `xml:"sub_cs,attr"`
	RespSt     string    `xml:"resp_st,attr"`
	RetrSt     string    `xml:"retr_st,attr"`
	DTm        string    `xml:"d_tm,attr"`
	TextOnly   string    `xml:"text_only,attr"`
	Exp        string    `xml:"exp,attr"`
	MID        string    `xml:"m_id,attr"`
	St         string    `xml:"st,attr"`
	RetrTxtCs  string    `xml:"retr_txt_cs,attr"`
	RetrTxt    string    `xml:"retr_txt,attr"`
	Creator    string    `xml:"creator,attr"`
	MSize      string    `xml:"m_size,attr"`
	RptA       string    `xml:"rpt_a,attr"`
	CtCls      string    `xml:"ct_cls,attr"`
	Pri        string    `xml:"pri,attr"`
	TrID       string    `xml:"tr_id,attr"`
	RespTxt    string    `xml:"resp_txt,attr"`
	CtL        string    `xml:"ct_l,attr"`
	MCls       string    `xml:"m_cls,attr"`
	DRpt       string    `xml:"d_rpt,attr"`
	V          string    `xml:"v,attr"`
	MType      string    `xml:"m_type,attr"`
	Parts      Parts     `xml:"parts"`
	Addrs      Addresses `xml:"addrs"`

	client *Client
}

// ID returns a unique ID by concatenating the
// date of the message with its TRID.
func (m MMS) ID() string {
	return fmt.Sprintf("%d_%s", m.Date, m.TrID)
}

// Timestamp returns the message's date.
func (m MMS) Timestamp() time.Time {
	return time.Unix(0, m.Date*int64(time.Millisecond))
}

// Class returns the class Message.
func (m MMS) Class() timeliner.ItemClass {
	return timeliner.ClassMessage
}

// Owner returns the name and number of the sender,
// if available. The export format does not give us
// the contacts' names, however.
func (m MMS) Owner() (number *string, name *string) {
	for _, addr := range m.Addrs.Addr {
		if addr.Type == mmsAddrTypeSender {
			// TODO: Get sender name... for group texts this is tricky/impossible, since order varies
			// TODO: If there is only one other contact on the message (other than the account owner's number), we can probably assume the contact name is theirs.

			standardized, err := m.client.standardizePhoneNumber(addr.Address)
			if err != nil {
				// oh well; just go with what we have, I guess
				return &addr.Address, nil
			}
			return &standardized, nil
		}
	}
	return nil, nil
}

// DataText returns the text of the multimedia message, if any.
func (m MMS) DataText() (*string, error) {
	var text string
	for _, part := range m.Parts.Part {
		if part.Seq < 0 {
			continue
		}
		if part.ContentType == "text/plain" &&
			part.AttrText != "" &&
			part.AttrText != "null" {
			text += part.AttrText
		}
	}
	if text != "" {
		return &text, nil
	}
	return nil, nil
}

// DataFileName returns the name of the file, if any.
func (m MMS) DataFileName() *string {
	for _, part := range m.Parts.Part {
		if part.Seq < 0 {
			continue
		}
		if isMediaContentType(part.ContentType) {
			return &part.Filename
		}
	}
	return nil
}

// DataFileReader returns the data file reader, if any.
func (m MMS) DataFileReader() (io.ReadCloser, error) {
	for _, part := range m.Parts.Part {
		if part.Seq < 0 {
			continue
		}
		if isMediaContentType(part.ContentType) {
			sr := strings.NewReader(part.Data)
			bd := base64.NewDecoder(base64.StdEncoding, sr)
			return timeliner.FakeCloser(bd), nil
		}
	}
	return nil, nil
}

// DataFileHash returns nil.
func (m MMS) DataFileHash() []byte {
	return nil
}

// DataFileMIMEType returns the MIME type, if any.
func (m MMS) DataFileMIMEType() *string {
	for _, part := range m.Parts.Part {
		if isMediaContentType(part.ContentType) {
			return &part.ContentType
		}
	}
	return nil
}

// Metadata returns nil.
func (m MMS) Metadata() (*timeliner.Metadata, error) {
	return nil, nil
}

// Location returns nil.
func (m MMS) Location() (*timeliner.Location, error) {
	return nil, nil
}

// Parts is the parts of an MMS.
type Parts struct {
	Text string `xml:",chardata"`
	Part []Part `xml:"part"`
}

// Part is a part of an MMS.
type Part struct {
	Text        string `xml:",chardata"`
	Seq         int    `xml:"seq,attr"`
	ContentType string `xml:"ct,attr"`
	Name        string `xml:"name,attr"`
	Charset     string `xml:"chset,attr"`
	Cd          string `xml:"cd,attr"`
	Fn          string `xml:"fn,attr"`
	Cid         string `xml:"cid,attr"`
	Filename    string `xml:"cl,attr"`
	CttS        string `xml:"ctt_s,attr"`
	CttT        string `xml:"ctt_t,attr"`
	AttrText    string `xml:"text,attr"`
	Data        string `xml:"data,attr"`
}

// Addresses is the addresses the MMS was sent to.
type Addresses struct {
	Text string    `xml:",chardata"`
	Addr []Address `xml:"addr"`
}

// Address is a sender or recipient of the MMS.
type Address struct {
	Text    string `xml:",chardata"`
	Address string `xml:"address,attr"`
	Type    int    `xml:"type,attr"` // 151 = recipient, 137 = sender
	Charset string `xml:"charset,attr"`
}

func isMediaContentType(ct string) bool {
	return strings.HasPrefix(ct, "image/") ||
		strings.HasPrefix(ct, "video/")
}

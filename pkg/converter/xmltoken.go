package converter

import (
	"bufio"
	"encoding/xml"
	"io"
	"strings"
)

// rawXMLWriter writes tokens obtained from xml.Decoder.RawToken back out,
// preserving namespace prefixes, attribute order and surrounding
// whitespace. A struct-based round trip (or a generic 3MF library) could
// not guarantee that for Bambu's proprietary, un-namespaced extensions.
type rawXMLWriter struct {
	w *bufio.Writer
	// pending holds the last StartElement so that an immediately following
	// matching EndElement can be collapsed to a self-closing tag.
	pending *xml.StartElement
}

func newRawXMLWriter(w io.Writer) *rawXMLWriter {
	return &rawXMLWriter{w: bufio.NewWriterSize(w, 1<<16)}
}

func rawName(n xml.Name) string {
	if n.Space != "" {
		return n.Space + ":" + n.Local
	}
	return n.Local
}

var attrEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"\n", "&#xA;",
	"\t", "&#x9;",
	"\r", "&#xD;",
)

var textEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
)

func (rw *rawXMLWriter) flushPending(selfClose bool) error {
	if rw.pending == nil {
		return nil
	}
	el := rw.pending
	rw.pending = nil
	if _, err := rw.w.WriteString("<" + rawName(el.Name)); err != nil {
		return err
	}
	for _, a := range el.Attr {
		if _, err := rw.w.WriteString(" " + rawName(a.Name) + `="` + attrEscaper.Replace(a.Value) + `"`); err != nil {
			return err
		}
	}
	if selfClose {
		_, err := rw.w.WriteString("/>")
		return err
	}
	_, err := rw.w.WriteString(">")
	return err
}

func (rw *rawXMLWriter) writeRaw(s string) error {
	if err := rw.flushPending(false); err != nil {
		return err
	}
	_, err := rw.w.WriteString(s)
	return err
}

func (rw *rawXMLWriter) writeToken(tok xml.Token) error {
	switch t := tok.(type) {
	case xml.StartElement:
		if err := rw.flushPending(false); err != nil {
			return err
		}
		// Copied because RawToken reuses its buffers across calls.
		c := xml.CopyToken(t).(xml.StartElement)
		rw.pending = &c
		return nil
	case xml.EndElement:
		if rw.pending != nil && rw.pending.Name == t.Name {
			return rw.flushPending(true)
		}
		return rw.writeRaw("</" + rawName(t.Name) + ">")
	case xml.CharData:
		return rw.writeRaw(textEscaper.Replace(string(t)))
	case xml.Comment:
		return rw.writeRaw("<!--" + string(t) + "-->")
	case xml.ProcInst:
		return rw.writeRaw("<?" + t.Target + " " + string(t.Inst) + "?>")
	case xml.Directive:
		return rw.writeRaw("<!" + string(t) + ">")
	}
	return nil
}

func (rw *rawXMLWriter) close() error {
	if err := rw.flushPending(false); err != nil {
		return err
	}
	return rw.w.Flush()
}

// rewriteXML streams src to dst; transform may edit StartElement attrs in
// place before they are written.
func rewriteXML(src io.Reader, dst io.Writer, transform func(el *xml.StartElement) error) error {
	dec := xml.NewDecoder(src)
	out := newRawXMLWriter(dst)
	for {
		tok, err := dec.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if el, ok := tok.(xml.StartElement); ok && transform != nil {
			if err := transform(&el); err != nil {
				return err
			}
			tok = el
		}
		if err := out.writeToken(tok); err != nil {
			return err
		}
	}
	return out.close()
}

package converter

import (
	"bufio"
	"encoding/xml"
	"io"
	"strings"
)

// rawXMLWriter writes tokens obtained from xml.Decoder.RawToken back out,
// preserving namespace prefixes, attribute order and surrounding whitespace.
// It is used to rewrite single attributes inside Bambu's .model and
// model_settings.config files without disturbing anything else, which a
// struct-based round trip (or a generic 3MF library) could not guarantee
// for Bambu's proprietary, un-namespaced extensions.
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

// writeToken emits one raw token. StartElement tokens are buffered so that
// empty elements stay self-closing; the token must remain valid until the
// next call (use xml.CopyToken when buffering elsewhere).
func (rw *rawXMLWriter) writeToken(tok xml.Token) error {
	switch t := tok.(type) {
	case xml.StartElement:
		if err := rw.flushPending(false); err != nil {
			return err
		}
		c := xml.CopyToken(t).(xml.StartElement)
		rw.pending = &c
		return nil
	case xml.EndElement:
		if rw.pending != nil && rw.pending.Name == t.Name {
			return rw.flushPending(true)
		}
		if err := rw.flushPending(false); err != nil {
			return err
		}
		_, err := rw.w.WriteString("</" + rawName(t.Name) + ">")
		return err
	case xml.CharData:
		if err := rw.flushPending(false); err != nil {
			return err
		}
		_, err := rw.w.WriteString(textEscaper.Replace(string(t)))
		return err
	case xml.Comment:
		if err := rw.flushPending(false); err != nil {
			return err
		}
		_, err := rw.w.WriteString("<!--" + string(t) + "-->")
		return err
	case xml.ProcInst:
		if err := rw.flushPending(false); err != nil {
			return err
		}
		_, err := rw.w.WriteString("<?" + t.Target + " " + string(t.Inst) + "?>")
		return err
	case xml.Directive:
		if err := rw.flushPending(false); err != nil {
			return err
		}
		_, err := rw.w.WriteString("<!" + string(t) + ">")
		return err
	}
	return nil
}

func (rw *rawXMLWriter) close() error {
	if err := rw.flushPending(false); err != nil {
		return err
	}
	return rw.w.Flush()
}

// rewriteXML streams src to dst, letting transform inspect and modify each
// StartElement before it is written. transform may edit attrs in place.
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

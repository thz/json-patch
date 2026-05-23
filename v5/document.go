package jsonpatch

import (
	"fmt"

	"github.com/thz/json-patch/v5/internal/json"
)

// Document is a parsed JSON document that can have multiple Patches applied
// in sequence without re-parsing or re-marshalling between them.
//
// Typical use is replaying a long history of patches against an
// accumulating document, where parse+marshal on every Apply call would
// dominate the cost:
//
//	d, err := jsonpatch.ParseDocument(doc, opts)
//	if err != nil { ... }
//	for _, p := range patches {
//	    if err := p.ApplyToDocument(d, opts); err != nil { ... }
//	}
//	out, err := d.Marshal()
//
// A Document is not safe for concurrent use.
type Document struct {
	inner container
	opts  *ApplyOptions
}

// ParseDocument parses doc into a reusable Document.
//
// opts is retained for use by Marshal (for EscapeHTML) and as the default
// for any ApplyToDocument call that passes nil opts. If opts is nil, the
// defaults from NewApplyOptions are used.
func ParseDocument(doc []byte, opts *ApplyOptions) (*Document, error) {
	if len(doc) == 0 {
		return nil, ErrInvalid
	}
	if opts == nil {
		opts = NewApplyOptions()
	}
	if !json.Valid(doc) {
		return nil, ErrInvalid
	}

	raw := json.RawMessage(doc)
	self := newLazyNode(&raw)

	var pd container
	if doc[0] == '[' {
		pd = &partialArray{self: self}
	} else {
		pd = &partialDoc{self: self, opts: opts}
	}
	if err := unmarshal(doc, pd); err != nil {
		return nil, err
	}
	return &Document{inner: pd, opts: opts}, nil
}

// Marshal serializes the current document state to JSON bytes.
func (d *Document) Marshal() ([]byte, error) {
	return json.MarshalEscaped(d.inner, d.opts.EscapeHTML)
}

// ApplyToDocument applies the patch to an already-parsed Document in place,
// avoiding the parse/marshal cycle on each call.
//
// Pass nil opts to inherit the options the Document was created with.
func (p Patch) ApplyToDocument(d *Document, opts *ApplyOptions) error {
	if opts == nil {
		opts = d.opts
	}
	var accumulatedCopySize int64
	for _, op := range p {
		var err error
		switch op.Kind() {
		case "add":
			err = p.add(&d.inner, op, opts)
		case "remove":
			err = p.remove(&d.inner, op, opts)
		case "replace":
			err = p.replace(&d.inner, op, opts)
		case "move":
			err = p.move(&d.inner, op, opts)
		case "test":
			err = p.test(&d.inner, op, opts)
		case "copy":
			err = p.copy(&d.inner, op, &accumulatedCopySize, opts)
		default:
			err = fmt.Errorf("Unexpected kind: %s", op.Kind())
		}
		if err != nil {
			return err
		}
	}
	return nil
}

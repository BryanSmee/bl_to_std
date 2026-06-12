// Package paint implements the codec for the per-triangle painting data
// stored by Bambu Studio and OrcaSlicer in 3MF model files (the
// "paint_color" attribute on <triangle> elements, also used for Prusa's
// "mmu_segmentation").
//
// The format originates in PrusaSlicer's TriangleSelector: each attribute
// value is a hex string encoding a bitstream that describes how the
// triangle is recursively split and which filament state each leaf carries.
//
// Bitstream grammar (bits are consumed least-significant-bit first, and the
// hex string stores the bitstream nibble-reversed, i.e. the LAST character
// of the string holds the FIRST four bits):
//
//	record   := splitSides(2 bits) body
//	body     := leafState                          when splitSides == 0
//	          | specialSide(2 bits) record{splitSides+1}
//	leafState:= state(2 bits)                      when state < 3
//	          | 0b11 ext(4 bits)*                  state = 3 + sum(ext...),
//	                                               each 0xF nibble adds 15
//	                                               and continues
//
// Leaf state semantics: 0 means "unpainted" (the triangle uses the
// object's/volume's default extruder); state N >= 1 means filament N
// (1-based).
package paint

import (
	"fmt"
	"strings"
)

// Node is one record of the triangle selection tree.
type Node struct {
	// SplitSides is the number of split sides (0..3). 0 means leaf.
	SplitSides int
	// SpecialSide is only meaningful when SplitSides is 1 or 2.
	SpecialSide int
	// State is the leaf paint state: 0 = unpainted, N = filament N.
	State int
	// Children holds SplitSides+1 nodes, in serialization order.
	Children []Node
}

type bitReader struct {
	bits []bool
	pos  int
}

func (r *bitReader) read(n int) (int, error) {
	if r.pos+n > len(r.bits) {
		return 0, fmt.Errorf("paint: bitstream truncated (want %d bits at offset %d of %d)", n, r.pos, len(r.bits))
	}
	v := 0
	for i := 0; i < n; i++ {
		if r.bits[r.pos+i] {
			v |= 1 << i
		}
	}
	r.pos += n
	return v, nil
}

// Decode parses a paint_color hex string into its selection tree.
func Decode(s string) (Node, error) {
	if s == "" {
		return Node{}, fmt.Errorf("paint: empty string")
	}
	bits := make([]bool, 0, len(s)*4)
	for i := len(s) - 1; i >= 0; i-- {
		c := s[i]
		var dec int
		switch {
		case c >= '0' && c <= '9':
			dec = int(c - '0')
		case c >= 'A' && c <= 'F':
			dec = int(c-'A') + 10
		case c >= 'a' && c <= 'f':
			dec = int(c-'a') + 10
		default:
			return Node{}, fmt.Errorf("paint: invalid hex character %q in %q", c, s)
		}
		for b := 0; b < 4; b++ {
			bits = append(bits, dec&(1<<b) != 0)
		}
	}
	r := &bitReader{bits: bits}
	n, err := decodeNode(r)
	if err != nil {
		return Node{}, fmt.Errorf("paint: %w (input %q)", err, s)
	}
	if r.pos != len(r.bits) {
		return Node{}, fmt.Errorf("paint: %d trailing bits after decoding %q", len(r.bits)-r.pos, s)
	}
	return n, nil
}

func decodeNode(r *bitReader) (Node, error) {
	splitSides, err := r.read(2)
	if err != nil {
		return Node{}, err
	}
	if splitSides == 0 {
		state, err := r.read(2)
		if err != nil {
			return Node{}, err
		}
		if state == 3 {
			for {
				ext, err := r.read(4)
				if err != nil {
					return Node{}, err
				}
				state += ext
				if ext != 15 {
					break
				}
			}
		}
		return Node{State: state}, nil
	}
	specialSide, err := r.read(2)
	if err != nil {
		return Node{}, err
	}
	n := Node{SplitSides: splitSides, SpecialSide: specialSide}
	n.Children = make([]Node, splitSides+1)
	for i := range n.Children {
		child, err := decodeNode(r)
		if err != nil {
			return Node{}, err
		}
		n.Children[i] = child
	}
	return n, nil
}

// Encode serializes the selection tree back to the uppercase hex string
// representation used in 3MF files.
func (n Node) Encode() string {
	var bits []bool
	bits = encodeNode(n, bits)
	// The grammar always produces a multiple of 4 bits.
	var sb strings.Builder
	for i := 0; i < len(bits); i += 4 {
		v := 0
		for b := 0; b < 4 && i+b < len(bits); b++ {
			if bits[i+b] {
				v |= 1 << b
			}
		}
		sb.WriteByte("0123456789ABCDEF"[v])
	}
	// Nibbles are stored in reverse order.
	out := []byte(sb.String())
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

func encodeNode(n Node, bits []bool) []bool {
	bits = append(bits, n.SplitSides&1 != 0, n.SplitSides&2 != 0)
	if n.SplitSides == 0 {
		s := n.State
		if s < 3 {
			bits = append(bits, s&1 != 0, s&2 != 0)
		} else {
			bits = append(bits, true, true)
			s -= 3
			for s >= 15 {
				bits = append(bits, true, true, true, true)
				s -= 15
			}
			for b := 0; b < 4; b++ {
				bits = append(bits, s&(1<<b) != 0)
			}
		}
		return bits
	}
	bits = append(bits, n.SpecialSide&1 != 0, n.SpecialSide&2 != 0)
	for _, c := range n.Children {
		bits = encodeNode(c, bits)
	}
	return bits
}

// Remap rewrites every painted leaf state through fn, which receives a
// 1-based filament number and returns the new 1-based filament number.
// Unpainted leaves (state 0) are left untouched. It returns the re-encoded
// hex string.
func Remap(s string, fn func(filament int) int) (string, error) {
	n, err := Decode(s)
	if err != nil {
		return "", err
	}
	n.remap(fn)
	return n.Encode(), nil
}

func (n *Node) remap(fn func(int) int) {
	if n.SplitSides == 0 {
		if n.State > 0 {
			if v := fn(n.State); v > 0 {
				n.State = v
			}
		}
		return
	}
	for i := range n.Children {
		n.Children[i].remap(fn)
	}
}

// Filaments reports the set of 1-based filament numbers painted anywhere in
// the tree. Unpainted leaves are not included.
func (n Node) Filaments() map[int]bool {
	out := map[int]bool{}
	n.collect(out)
	return out
}

func (n Node) collect(out map[int]bool) {
	if n.SplitSides == 0 {
		if n.State > 0 {
			out[n.State] = true
		}
		return
	}
	for _, c := range n.Children {
		c.collect(out)
	}
}

package gguf

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
)

const (
	magic        = 0x46554747 // "GGUF" little-endian
	firstChunk   = 64 * 1024
	maxChunk     = 4 * 1024 * 1024
	maxStringLen = 1 << 24
	maxArrayKeep = 1 << 16
)

// GGUF value types.
const (
	tU8 = iota
	tI8
	tU16
	tI16
	tU32
	tI32
	tF32
	tBool
	tString
	tArray
	tU64
	tI64
	tF64
)

// stream is a forward-only reader over a Source that fetches in growing
// chunks, so a typical header costs a single 64 KiB request and skipping
// over data costs nothing.
type stream struct {
	src   Source
	pos   int64 // logical position
	buf   []byte
	bufAt int64 // file offset of buf[0]
	chunk int
}

func newStream(src Source) *stream { return &stream{src: src, chunk: firstChunk} }

func (s *stream) read(n int) ([]byte, error) {
	if n < 0 || int64(n) > s.src.Size() {
		return nil, fmt.Errorf("invalid read length %d", n)
	}
	end := s.pos + int64(n)
	if end > s.src.Size() {
		return nil, io.ErrUnexpectedEOF
	}
	if s.pos < s.bufAt || end > s.bufAt+int64(len(s.buf)) {
		size := s.chunk
		if n > size {
			size = n
		}
		if rem := s.src.Size() - s.pos; int64(size) > rem {
			size = int(rem)
		}
		b := make([]byte, size)
		got, err := s.src.ReadAt(b, s.pos)
		if err != nil && !(errors.Is(err, io.EOF) && got == size) {
			return nil, err
		}
		s.buf, s.bufAt = b, s.pos
		if s.chunk < maxChunk {
			s.chunk *= 2
		}
	}
	out := s.buf[s.pos-s.bufAt : end-s.bufAt]
	s.pos = end
	return out, nil
}

func (s *stream) skip(n uint64) error {
	if n > uint64(s.src.Size()) || s.pos+int64(n) > s.src.Size() {
		return io.ErrUnexpectedEOF
	}
	s.pos += int64(n)
	return nil
}

func (s *stream) u32() (uint32, error) {
	b, err := s.read(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

func (s *stream) u64() (uint64, error) {
	b, err := s.read(8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(b), nil
}

type parser struct {
	*stream
	version uint32
}

func (p *parser) str() (string, error) {
	n, err := p.u64()
	if err != nil {
		return "", err
	}
	if n > maxStringLen {
		return "", fmt.Errorf("string too long (%d)", n)
	}
	b, err := p.read(int(n))
	return string(b), err
}

func scalarSize(t uint32) int {
	switch t {
	case tU8, tI8, tBool:
		return 1
	case tU16, tI16:
		return 2
	case tU32, tI32, tF32:
		return 4
	case tU64, tI64, tF64:
		return 8
	}
	return 0
}

// scalar decodes a value: integers/bools as uint64, floats as float64,
// strings as string.
func (p *parser) scalar(t uint32) (any, error) {
	if t == tString {
		return p.str()
	}
	n := scalarSize(t)
	if n == 0 {
		return nil, fmt.Errorf("unsupported value type %d", t)
	}
	b, err := p.read(n)
	if err != nil {
		return nil, err
	}
	switch t {
	case tU8, tBool:
		return uint64(b[0]), nil
	case tI8:
		return uint64(int64(int8(b[0]))), nil
	case tU16:
		return uint64(binary.LittleEndian.Uint16(b)), nil
	case tI16:
		return uint64(int64(int16(binary.LittleEndian.Uint16(b)))), nil
	case tU32:
		return uint64(binary.LittleEndian.Uint32(b)), nil
	case tI32:
		return uint64(int64(int32(binary.LittleEndian.Uint32(b)))), nil
	case tU64, tI64:
		return binary.LittleEndian.Uint64(b), nil
	case tF32:
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(b))), nil
	default:
		return math.Float64frombits(binary.LittleEndian.Uint64(b)), nil
	}
}

// value reads one value. When keep is false, arrays are skipped (nil result).
func (p *parser) value(t uint32, keep bool) (any, error) {
	if t != tArray {
		return p.scalar(t)
	}
	et, err := p.u32()
	if err != nil {
		return nil, err
	}
	cnt, err := p.u64()
	if err != nil {
		return nil, err
	}
	if sz := scalarSize(et); sz > 0 {
		if !keep || cnt > maxArrayKeep {
			return nil, p.skip(cnt * uint64(sz))
		}
		out := make([]uint64, 0, cnt)
		for i := uint64(0); i < cnt; i++ {
			v, err := p.scalar(et)
			if err != nil {
				return nil, err
			}
			if u, ok := v.(uint64); ok {
				out = append(out, u)
			}
		}
		return out, nil
	}
	if et != tString {
		return nil, fmt.Errorf("unsupported array element type %d", et)
	}
	for i := uint64(0); i < cnt; i++ { // walk string lengths, skipping bodies
		n, err := p.u64()
		if err != nil {
			return nil, err
		}
		if err := p.skip(n); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func asU32(v any) (uint32, bool) {
	switch x := v.(type) {
	case uint64:
		return uint32(x), true
	case float64:
		return uint32(x), true
	}
	return 0, false
}

// Read parses the GGUF header of src (versions 2 and 3) and returns the
// sizing metadata. FileSize is src.Size(); callers handling shards adjust it.
func Read(src Source) (*Metadata, error) {
	p := &parser{stream: newStream(src)}

	mg, err := p.u32()
	if err != nil {
		return nil, fmt.Errorf("reading magic: %w", err)
	}
	if mg != magic {
		return nil, errors.New("not a GGUF file (bad magic)")
	}
	if p.version, err = p.u32(); err != nil {
		return nil, err
	}
	if p.version < 2 || p.version > 3 {
		return nil, fmt.Errorf("unsupported GGUF version %d", p.version)
	}
	if _, err = p.u64(); err != nil { // tensor count
		return nil, err
	}
	kvCount, err := p.u64()
	if err != nil {
		return nil, err
	}

	m := &Metadata{FileSize: uint64(src.Size())}
	var arch string
	var kvHeads, heads any // scalar (uint64) or []uint64
	have := func() bool { return m.NLayers > 0 && m.NEmbd > 0 && heads != nil }

	for i := uint64(0); i < kvCount; i++ {
		key, err := p.str()
		if err != nil {
			return nil, fmt.Errorf("reading key %d: %w", i, err)
		}
		t, err := p.u32()
		if err != nil {
			return nil, err
		}
		// Tokenizer data follows the architecture keys and can span megabytes;
		// once the core hyperparameters are known there is nothing left to fetch.
		if strings.HasPrefix(key, "tokenizer.") && have() {
			break
		}
		keep := strings.HasPrefix(key, "general.") || (arch != "" && strings.HasPrefix(key, arch+"."))
		v, err := p.value(t, keep)
		if err != nil {
			return nil, fmt.Errorf("reading value of %q: %w", key, err)
		}
		switch {
		case key == "general.architecture":
			arch, _ = v.(string)
			m.Arch = arch
		case key == "general.name":
			m.Name, _ = v.(string)
		case key == "general.file_type":
			m.FileType, _ = asU32(v)
		case arch != "" && strings.HasPrefix(key, arch+"."):
			switch strings.TrimPrefix(key, arch+".") {
			case "block_count":
				m.NLayers, _ = asU32(v)
			case "embedding_length":
				m.NEmbd, _ = asU32(v)
			case "context_length":
				m.ContextTrain, _ = asU32(v)
			case "attention.head_count":
				heads = v
			case "attention.head_count_kv":
				kvHeads = v
			case "attention.key_length":
				m.KeyLength, _ = asU32(v)
			case "attention.value_length":
				m.ValueLength, _ = asU32(v)
			}
		}
	}

	if m.Arch == "" || m.NLayers == 0 || m.NEmbd == 0 || heads == nil {
		return nil, errors.New("GGUF header lacks required keys (architecture, block_count, embedding_length, attention.head_count)")
	}
	perLayer := func(v any, def uint32) []uint32 {
		out := make([]uint32, m.NLayers)
		for i := range out {
			switch x := v.(type) {
			case uint64:
				out[i] = uint32(x)
			case []uint64:
				switch {
				case len(x) == 0:
					out[i] = def
				case i < len(x):
					out[i] = uint32(x[i])
				default:
					out[i] = uint32(x[len(x)-1])
				}
			default:
				out[i] = def
			}
		}
		return out
	}
	for _, h := range perLayer(heads, 0) {
		if h > m.NHeads {
			m.NHeads = h
		}
	}
	if m.NHeads == 0 {
		return nil, errors.New("attention.head_count is zero")
	}
	m.NKVHeads = perLayer(kvHeads, m.NHeads) // absent => MHA
	if m.KeyLength == 0 {
		m.KeyLength = m.NEmbd / m.NHeads
	}
	if m.ValueLength == 0 {
		m.ValueLength = m.KeyLength
	}
	m.Quant = QuantName(m.FileType)
	return m, nil
}

package dbus

import (
	"bytes"
	"encoding/binary"
	"io"
	"reflect"
	"unicode"
)

// An Encoder encodes values to the DBus wire format.
type Encoder struct {
	out   io.Writer
	order binary.ByteOrder
	pos   int
}

// NewEncoder returns a new encoder that writes to out in the given byte order.
func NewEncoder(out io.Writer, order binary.ByteOrder) *Encoder {
	enc := new(Encoder)
	enc.out = out
	enc.order = order
	return enc
}

// Aligns the next output to be on a multiple of n. Panics on write errors.
func (enc *Encoder) align(n int) {
	newpos := enc.pos
	if newpos%n != 0 {
		newpos += (n - (newpos % n))
	}
	empty := make([]byte, newpos-enc.pos)
	if _, err := enc.out.Write(empty); err != nil {
		panic(err)
	}
	enc.pos = newpos
}

// Calls binary.Write(enc.out, enc.order, v) and panics on write errors.
func (enc *Encoder) binwrite(v interface{}) {
	if err := binary.Write(enc.out, enc.order, v); err != nil {
		panic(err)
	}
}

// Encode encodes a single value to the underyling reader. All written values
// are aligned properly as required by the DBus spec.
func (enc *Encoder) Encode(v interface{}) (err error) {
	defer func() {
		err, ok := recover().(error)
		if ok {
			// invalidTypeErrors are errors in the program and can't really be
			// recovered from
			if _, ok := err.(invalidTypeError); ok {
				panic(err)
			}
		}
	}()
	enc.encode(reflect.ValueOf(v))
	return nil
}

// Encode is a shorthand for multiple Encode calls.
func (enc *Encoder) EncodeMulti(vs ...interface{}) error {
	for _, v := range vs {
		if err := enc.Encode(v); err != nil {
			return err
		}
	}
	return nil
}

// encode encodes the given value to the writer and panics on error.
func (enc *Encoder) encode(v reflect.Value) {
	enc.align(alignment(v.Type()))
	switch v.Kind() {
	case reflect.Uint8:
		if _, err := enc.out.Write([]byte{byte(v.Uint())}); err != nil {
			panic(err)
		}
		enc.pos++
	case reflect.Bool:
		if v.Bool() {
			enc.encode(reflect.ValueOf(uint32(1)))
		} else {
			enc.encode(reflect.ValueOf(uint32(0)))
		}
	case reflect.Int16:
		enc.binwrite(int16(v.Int()))
		enc.pos += 2
	case reflect.Uint16:
		enc.binwrite(uint16(v.Uint()))
		enc.pos += 2
	case reflect.Int32:
		enc.binwrite(int32(v.Int()))
		enc.pos += 4
	case reflect.Uint32:
		enc.binwrite(uint32(v.Uint()))
		enc.pos += 4
	case reflect.Int64:
		enc.binwrite(v.Int())
		enc.pos += 8
	case reflect.Uint64:
		enc.binwrite(v.Uint())
		enc.pos += 8
	case reflect.Float64:
		enc.binwrite(v.Float())
		enc.pos += 8
	case reflect.String:
		enc.encode(reflect.ValueOf(uint32(len(v.String()))))
		n, err := enc.out.Write([]byte(v.String() + "\x00"))
		if err != nil {
			panic(err)
		}
		enc.pos += n
	case reflect.Ptr:
		enc.encode(v.Elem())
	case reflect.Slice, reflect.Array:
		buf := new(bytes.Buffer)
		bufenc := NewEncoder(buf, enc.order)

		for i := 0; i < v.Len(); i++ {
			bufenc.encode(v.Index(i))
		}
		enc.encode(reflect.ValueOf(uint32(buf.Len())))
		length := buf.Len()
		enc.align(alignment(v.Type().Elem()))
		if _, err := buf.WriteTo(enc.out); err != nil {
			panic(err)
		}
		enc.pos += length
	case reflect.Struct:
		switch t := v.Type(); t {
		case signatureType:
			str := v.Field(0)
			enc.encode(reflect.ValueOf(byte(str.Len())))
			n, err := enc.out.Write([]byte(str.String() + "\x00"))
			if err != nil {
				panic(err)
			}
			enc.pos += n
		case variantType:
			variant := v.Interface().(Variant)
			enc.encode(reflect.ValueOf(variant.sig))
			enc.encode(reflect.ValueOf(variant.value))
		default:
			for i := 0; i < v.Type().NumField(); i++ {
				field := t.Field(i)
				if unicode.IsUpper([]rune(field.Name)[0]) &&
					field.Tag.Get("dbus") != "-" {

					enc.encode(v.Field(i))
				}
			}
		}
	case reflect.Map:
		if !isKeyType(v.Type().Key()) {
			panic(invalidTypeError{v.Type()})
		}
		keys := v.MapKeys()
		buf := new(bytes.Buffer)
		bufenc := NewEncoder(buf, enc.order)
		for _, k := range keys {
			bufenc.align(8)
			bufenc.encode(k)
			bufenc.encode(v.MapIndex(k))
		}
		enc.encode(reflect.ValueOf(uint32(buf.Len())))
		length := buf.Len()
		enc.align(8)
		if _, err := buf.WriteTo(enc.out); err != nil {
			panic(err)
		}
		enc.pos += length
	default:
		panic(invalidTypeError{v.Type()})
	}
}

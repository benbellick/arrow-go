// Licensed to the Apache Software Foundation (ASF) under one
// or more contributor license agreements.  See the NOTICE file
// distributed with this work for additional information
// regarding copyright ownership.  The ASF licenses this file
// to you under the Apache License, Version 2.0 (the
// "License"); you may not use this file except in compliance
// with the License.  You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build go1.18

package exprs

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/bitutil"
	"github.com/apache/arrow-go/v18/arrow/compute"
	"github.com/apache/arrow-go/v18/arrow/internal"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/arrow-go/v18/arrow/scalar"
	"github.com/substrait-io/substrait-go/v8/expr"
	"github.com/substrait-io/substrait-go/v8/types"
)

func getFields(typ arrow.DataType) []arrow.Field {
	if nested, ok := typ.(arrow.NestedType); ok {
		return nested.Fields()
	}
	return nil
}

// GetRefField evaluates the substrait field reference to retrieve the
// referenced field or return an error.
func GetRefField(ref expr.ReferenceSegment, fields []arrow.Field) (*arrow.Field, error) {
	if ref == nil {
		return nil, compute.ErrEmpty
	}

	var (
		out *arrow.Field
	)

	for ref != nil {
		if len(fields) == 0 {
			if out == nil {
				return nil, compute.ErrNoChildren
			}
			return nil, fmt.Errorf("%w: %s", compute.ErrNoChildren, out.Type)
		}

		switch f := ref.(type) {
		case *expr.StructFieldRef:
			if f.Field < 0 || f.Field >= int32(len(fields)) {
				return nil, fmt.Errorf("%w: indices=%s", compute.ErrIndexRange, f)
			}

			out = &fields[f.Field]
			fields = getFields(out.Type)
		default:
			return nil, arrow.ErrNotImplemented
		}

		ref = ref.GetChild()
	}

	return out, nil
}

// GetRefSchema evaluates the provided substrait field reference against
// the schema to retrieve the referenced (potentially nested) field.
func GetRefSchema(ref expr.ReferenceSegment, schema *arrow.Schema) (*arrow.Field, error) {
	return GetRefField(ref, schema.Fields())
}

// GetScalar returns the evaluated referenced scalar value from the provided
// scalar which must be appropriate to the type of reference.
//
// A StructFieldRef can only reference against a Struct-type scalar, a
// ListElementRef can only reference against a List or LargeList scalar,
// and a MapKeyRef will only reference against a Map scalar. An error is
// returned if following the reference children ends up with an invalid
// nested reference object.
//
// The result is a new reference: if it implements scalar.Releasable, the
// caller must release it.
func GetScalar(ref expr.ReferenceSegment, s scalar.Scalar, mem memory.Allocator, ext ExtensionIDSet) (scalar.Scalar, error) {
	if ref == nil {
		return nil, compute.ErrEmpty
	}

	// once owned is set, s is a reference this function must release when it
	// moves past it or fails
	owned := false
	for ; ref != nil; ref = ref.GetChild() {
		next, borrowed, err := scalarStep(ref, s, mem, ext)
		if err == nil && borrowed {
			retainScalar(next)
		}
		if owned {
			releaseScalar(s)
		}
		if err != nil {
			return nil, err
		}
		s, owned = next, true
	}
	return s, nil
}

// scalarStep follows one reference segment from s. borrowed reports whether
// next is part of s, rather than a new scalar the caller owns.
func scalarStep(ref expr.ReferenceSegment, s scalar.Scalar, mem memory.Allocator, ext ExtensionIDSet) (next scalar.Scalar, borrowed bool, err error) {
	switch f := ref.(type) {
	case *expr.StructFieldRef:
		if s.DataType().ID() != arrow.STRUCT {
			return nil, false, fmt.Errorf("%w: attempting to reference field from non-struct scalar %s",
				arrow.ErrInvalid, s)
		}

		st := s.(*scalar.Struct)
		fields := st.Type.(*arrow.StructType).Fields()
		if f.Field < 0 || f.Field >= int32(len(fields)) {
			return nil, false, fmt.Errorf("%w: indices=%s", compute.ErrIndexRange, ref)
		}
		if !st.IsValid() {
			return scalar.MakeNullScalar(fields[f.Field].Type), false, nil
		}
		return st.Value[f.Field], true, nil
	case *expr.ListElementRef:
		var values arrow.Array
		switch v := s.(type) {
		case *scalar.List:
			values = v.Value
		case *scalar.LargeList:
			values = v.Value
		default:
			return nil, false, fmt.Errorf("%w: cannot get ListElementRef from non-list scalar %s",
				arrow.ErrInvalid, s)
		}
		if !s.IsValid() {
			return scalar.MakeNullScalar(s.DataType().(arrow.ListLikeType).Elem()), false, nil
		}
		sc, err := scalar.GetScalar(values, int(f.Offset))
		return sc, false, err
	case *expr.MapKeyRef:
		v, ok := s.(*scalar.Map)
		if !ok {
			return nil, false, arrow.ErrInvalid
		}

		dt, _, err := FromSubstraitType(f.MapKey.GetType(), ext)
		if err != nil {
			return nil, false, err
		}

		mapType := v.Type.(*arrow.MapType)
		if !arrow.TypeEqual(dt, mapType.KeyType()) {
			return nil, false, arrow.ErrInvalid
		}
		if !v.IsValid() {
			return scalar.MakeNullScalar(mapType.ItemType()), false, nil
		}

		keyvalDatum, err := literalToDatum(mem, f.MapKey, ext)
		if err != nil {
			return nil, false, err
		}
		defer keyvalDatum.Release()

		var (
			keyval = keyvalDatum.(*compute.ScalarDatum)
			m      = v.Value.(*array.Struct)
			keys   = m.Field(0)
		)
		for i := 0; i < v.Value.Len(); i++ {
			kv, err := scalar.GetScalar(keys, i)
			if err != nil {
				return nil, false, err
			}
			found := scalar.Equals(kv, keyval.Value)
			releaseScalar(kv)
			if found {
				sc, err := scalar.GetScalar(m.Field(1), i)
				return sc, false, err
			}
		}
		return nil, false, arrow.ErrNotFound
	}
	return nil, false, arrow.ErrNotImplemented
}

func retainScalar(s scalar.Scalar) {
	if r, ok := s.(scalar.Releasable); ok {
		r.Retain()
	}
}

func releaseScalar(s scalar.Scalar) {
	if r, ok := s.(scalar.Releasable); ok {
		r.Release()
	}
}

// GetReferencedValue retrieves the referenced (potentially nested) value from
// the provided datum which may be a scalar, array, or record batch. The
// result is a new reference that the caller must release.
func GetReferencedValue(mem memory.Allocator, ref expr.ReferenceSegment, value compute.Datum, ext ExtensionIDSet) (compute.Datum, error) {
	if ref == nil {
		return nil, compute.ErrEmpty
	}

	// once owned is set, cur holds a reference this function must release
	// when it moves past it or fails
	cur, owned := value, false
	for ; ref != nil; ref = ref.GetChild() {
		var (
			next compute.Datum
			err  error
		)
		switch v := cur.(type) {
		case *compute.ScalarDatum:
			var (
				sc       scalar.Scalar
				borrowed bool
			)
			if sc, borrowed, err = scalarStep(ref, v.Value, mem, ext); err == nil {
				if borrowed {
					retainScalar(sc)
				}
				next = &compute.ScalarDatum{Value: sc}
			}
		case *compute.ArrayDatum:
			next, err = arrayStep(mem, ref, v)
		case *compute.RecordDatum:
			next, err = recordStep(ref, v)
		default:
			err = arrow.ErrNotImplemented
		}

		if owned {
			cur.Release()
		}
		if err != nil {
			return nil, err
		}
		cur, owned = next, true
	}
	return cur, nil
}

// arrayStep follows one reference segment from an array, returning a new
// reference.
func arrayStep(mem memory.Allocator, ref expr.ReferenceSegment, v *compute.ArrayDatum) (compute.Datum, error) {
	switch r := ref.(type) {
	case *expr.StructFieldRef:
		if v.Type().ID() != arrow.STRUCT {
			return nil, fmt.Errorf("%w: struct field ref for non struct type %s",
				arrow.ErrInvalid, v.Type())
		}
		if r.Field < 0 || r.Field >= int32(len(v.Value.Children())) {
			return nil, fmt.Errorf("%w: indices=%s", compute.ErrIndexRange, ref)
		}
		field, err := structField(mem, v.Value, int(r.Field))
		if err != nil {
			return nil, err
		}
		return &compute.ArrayDatum{Value: field}, nil
	case *expr.ListElementRef:
		switch v.Type().ID() {
		case arrow.LIST, arrow.LARGE_LIST, arrow.LIST_VIEW, arrow.LARGE_LIST_VIEW, arrow.FIXED_SIZE_LIST:
		default:
			return nil, fmt.Errorf("%w: cannot reference list element in non-list array type %s",
				arrow.ErrInvalid, v.Type())
		}
		// No caller context here: the default exec context keeps the result in one
		// chunk, while a caller's chunk size could split it and later steps reject that.
		ctx := compute.WithAllocator(context.Background(), mem)
		return compute.ListElement(ctx, v, compute.NewDatum(scalar.NewInt32Scalar(r.Offset)))
	}
	return nil, arrow.ErrNotImplemented
}

// recordStep follows a struct field reference into a record batch column,
// returning a new reference.
func recordStep(ref expr.ReferenceSegment, v *compute.RecordDatum) (compute.Datum, error) {
	r, ok := ref.(*expr.StructFieldRef)
	if !ok {
		return nil, arrow.ErrNotImplemented
	}
	if r.Field < 0 || r.Field >= int32(v.Value.NumCols()) {
		return nil, fmt.Errorf("%w: indices=%s", compute.ErrIndexRange, ref)
	}
	col := v.Value.Column(int(r.Field)).Data()
	col.Retain()
	return &compute.ArrayDatum{Value: col}, nil
}

// structField returns field i of a struct array as a new array: sliced to
// the struct's offset and length, and null wherever the struct is null.
func structField(mem memory.Allocator, parent arrow.ArrayData, i int) (arrow.ArrayData, error) {
	off, n := parent.Offset(), parent.Len()
	child := array.NewSliceData(parent.Children()[i], int64(off), int64(off+n))

	if parent.NullN() == 0 || len(parent.Buffers()) == 0 || parent.Buffers()[0] == nil {
		return child, nil
	}
	parentBits := parent.Buffers()[0].Bytes()
	parentNulls := parent.NullN()
	if parentNulls == array.UnknownNullCount {
		parentNulls = n - bitutil.CountSetBits(parentBits, off, n)
	}
	if parentNulls == 0 {
		return child, nil
	}

	storage := child.DataType()
	if ext, ok := storage.(arrow.ExtensionType); ok {
		storage = ext.StorageType()
	}
	if storage.ID() == arrow.NULL {
		return child, nil
	}
	if !internal.DefaultHasValidityBitmap(storage.ID()) {
		err := fmt.Errorf("%w: cannot propagate struct nulls to field type %s", arrow.ErrNotImplemented, child.DataType())
		child.Release()
		return nil, err
	}
	defer child.Release()

	// keep the child's offset so its other buffers stay valid
	cOff := child.Offset()
	bits := memory.NewResizableBuffer(mem)
	defer bits.Release()
	bits.Resize(int(bitutil.BytesForBits(int64(cOff + n))))
	if cb := child.Buffers()[0]; cb != nil {
		bitutil.BitmapAnd(parentBits, cb.Bytes(), int64(off), int64(cOff), bits.Bytes(), int64(cOff), int64(n))
	} else {
		bitutil.CopyBitmap(parentBits, off, n, bits.Bytes(), cOff)
	}
	nulls := n - bitutil.CountSetBits(bits.Bytes(), cOff, n)

	buffers := slices.Clone(child.Buffers())
	buffers[0] = bits
	out := array.NewData(child.DataType(), n, buffers, child.Children(), nulls, cOff)
	if dict := child.Dictionary(); dict != nil {
		out.SetDictionary(dict)
	}
	return out, nil
}

// NewFieldRefFromDotPath constructs a substrait reference segment from
// a dot path and the base schema.
//
// dot_path = '.' name
//
//	| '[' digit+ ']'
//	| dot_path+
//
// # Examples
//
// Assume root schema of {alpha: i32, beta: struct<gamma: list<i32>>, delta: map<string, i32>}
//
//	".alpha" => StructFieldRef(0)
//	"[2]" => StructFieldRef(2)
//	".beta[0]" => StructFieldRef(1, StructFieldRef(0))
//	"[1].gamma[3]" => StructFieldRef(1, StructFieldRef(0, ListElementRef(3)))
//	".delta.foobar" => StructFieldRef(2, MapKeyRef("foobar"))
//
// Note: when parsing a name, a '\' preceding any other character
// will be dropped from the resulting name. Therefore if a name must
// contain the characters '.', '\', '[', or ']' then they must be escaped
// with a preceding '\'.
func NewFieldRefFromDotPath(dotpath string, rootSchema *arrow.Schema) (expr.ReferenceSegment, error) {
	if len(dotpath) == 0 {
		return nil, fmt.Errorf("%w dotpath was empty", arrow.ErrInvalid)
	}

	parseName := func() string {
		var name string
		for {
			idx := strings.IndexAny(dotpath, `\[.`)
			if idx == -1 {
				name += dotpath
				dotpath = ""
				break
			}

			if dotpath[idx] != '\\' {
				// subscript for a new field ref
				name += dotpath[:idx]
				dotpath = dotpath[idx:]
				break
			}

			if len(dotpath) == idx+1 {
				// dotpath ends with a backslash; consume it all
				name += dotpath
				dotpath = ""
				break
			}

			// append all characters before backslash, then the character which follows it
			name += dotpath[:idx] + string(dotpath[idx+1])
			dotpath = dotpath[idx+2:]
		}
		return name
	}

	var curType arrow.DataType = arrow.StructOf(rootSchema.Fields()...)
	children := make([]expr.ReferenceSegment, 0)

	for len(dotpath) > 0 {
		subscript := dotpath[0]
		dotpath = dotpath[1:]
		switch subscript {
		case '.':
			// next element is a name
			n := parseName()
			switch ct := curType.(type) {
			case *arrow.StructType:
				idx, found := ct.FieldIdx(n)
				if !found {
					return nil, fmt.Errorf("%w: dot path '%s' referenced invalid field", arrow.ErrInvalid, dotpath)
				}
				children = append(children, &expr.StructFieldRef{Field: int32(idx)})
				curType = ct.Field(idx).Type
			case *arrow.MapType:
				curType = ct.KeyType()
				switch ct.KeyType().ID() {
				case arrow.BINARY, arrow.LARGE_BINARY:
					children = append(children, &expr.MapKeyRef{MapKey: expr.NewByteSliceLiteral([]byte(n), false)})
				case arrow.STRING, arrow.LARGE_STRING:
					children = append(children, &expr.MapKeyRef{MapKey: expr.NewPrimitiveLiteral(n, false)})
				default:
					return nil, fmt.Errorf("%w: MapKeyRef to non-binary/string map not supported", arrow.ErrNotImplemented)
				}
			default:
				return nil, fmt.Errorf("%w: dot path names must refer to struct fields or map keys", arrow.ErrInvalid)
			}
		case '[':
			subend := strings.IndexFunc(dotpath, func(r rune) bool { return !unicode.IsDigit(r) })
			if subend == -1 || dotpath[subend] != ']' {
				return nil, fmt.Errorf("%w: dot path '%s' contained an unterminated index", arrow.ErrInvalid, dotpath)
			}
			idx, _ := strconv.Atoi(dotpath[:subend])
			switch ct := curType.(type) {
			case *arrow.StructType:
				if idx > ct.NumFields() {
					return nil, fmt.Errorf("%w: field out of bounds in dotpath", arrow.ErrIndex)
				}
				curType = ct.Field(idx).Type
				children = append(children, &expr.StructFieldRef{Field: int32(idx)})
			case *arrow.MapType:
				curType = ct.KeyType()
				var keyLiteral expr.Literal
				// TODO: implement user defined types and variations
				switch ct.KeyType().ID() {
				case arrow.INT8:
					keyLiteral = expr.NewPrimitiveLiteral(int8(idx), false)
				case arrow.INT16:
					keyLiteral = expr.NewPrimitiveLiteral(int16(idx), false)
				case arrow.INT32:
					keyLiteral = expr.NewPrimitiveLiteral(int32(idx), false)
				case arrow.INT64:
					keyLiteral = expr.NewPrimitiveLiteral(int64(idx), false)
				case arrow.FLOAT32:
					keyLiteral = expr.NewPrimitiveLiteral(float32(idx), false)
				case arrow.FLOAT64:
					keyLiteral = expr.NewPrimitiveLiteral(float64(idx), false)
				default:
					return nil, fmt.Errorf("%w: dotpath ref to map key type %s", arrow.ErrNotImplemented, ct.KeyType())
				}
				children = append(children, &expr.MapKeyRef{MapKey: keyLiteral})
			case *arrow.ListType:
				curType = ct.Elem()
				children = append(children, &expr.ListElementRef{Offset: int32(idx)})
			case *arrow.LargeListType:
				curType = ct.Elem()
				children = append(children, &expr.ListElementRef{Offset: int32(idx)})
			case *arrow.FixedSizeListType:
				curType = ct.Elem()
				children = append(children, &expr.ListElementRef{Offset: int32(idx)})
			default:
				return nil, fmt.Errorf("%w: %s type not supported for dotpath ref", arrow.ErrInvalid, ct)
			}
			dotpath = dotpath[subend+1:]
		default:
			return nil, fmt.Errorf("%w: dot path must begin with '[' or '.' got '%s'",
				arrow.ErrInvalid, dotpath)
		}
	}

	out := children[0]
	if len(children) > 1 {
		cur := out
		for _, c := range children[1:] {
			switch r := cur.(type) {
			case *expr.StructFieldRef:
				r.Child = c
			case *expr.MapKeyRef:
				r.Child = c
			case *expr.ListElementRef:
				r.Child = c
			}
			cur = c
		}
	}

	return out, nil
}

// RefFromFieldPath constructs a substrait field reference segment
// from a compute.FieldPath which should be a slice of integers
// indicating nested field paths to travel. This will return a
// series of StructFieldRef's whose child is the next element in
// the field path.
func RefFromFieldPath(field compute.FieldPath) expr.ReferenceSegment {
	if len(field) == 0 {
		return nil
	}

	seg := expr.NewStructFieldRef(int32(field[0]))
	parent := seg
	for _, ref := range field[1:] {
		next := expr.NewStructFieldRef(int32(ref))
		parent.Child = next
		parent = next
	}

	return seg
}

// NewFieldRef constructs a properly typed substrait field reference segment,
// from a given arrow field reference, schema and extension set (for resolving
// substrait types).
func NewFieldRef(ref compute.FieldRef, schema *arrow.Schema, ext ExtensionIDSet) (*expr.FieldReference, error) {
	path, err := ref.FindOne(schema)
	if err != nil {
		return nil, err
	}

	st, err := ToSubstraitType(arrow.StructOf(schema.Fields()...), false, ext)
	if err != nil {
		return nil, err
	}

	return expr.NewRootFieldRef(RefFromFieldPath(path),
		types.NewRecordTypeFromStruct(*st.(*types.StructType)))
}

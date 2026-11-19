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

package shared

import (
	"fmt"
	"slices"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/bitutil"
	"github.com/apache/arrow-go/v18/arrow/internal"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/arrow-go/v18/arrow/scalar"
)

// ChildFields returns the child fields of a nested type, or nil.
func ChildFields(typ arrow.DataType) []arrow.Field {
	if nested, ok := typ.(arrow.NestedType); ok {
		return nested.Fields()
	}
	return nil
}

// RetainScalar retains s if it is reference counted.
func RetainScalar(s scalar.Scalar) {
	if r, ok := s.(scalar.Releasable); ok {
		r.Retain()
	}
}

// ReleaseScalar releases s if it is reference counted.
func ReleaseScalar(s scalar.Scalar) {
	if r, ok := s.(scalar.Releasable); ok {
		r.Release()
	}
}

// StructField returns field i of a struct array as a new array: sliced to
// the struct's offset and length, and null wherever the struct is null.
func StructField(mem memory.Allocator, parent arrow.ArrayData, i int) (arrow.ArrayData, error) {
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

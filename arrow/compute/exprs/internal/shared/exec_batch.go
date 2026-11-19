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

// Package shared holds the parts of the exprs packages that do not depend on
// a particular substrait-go major version.
package shared

import (
	"context"
	"fmt"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/compute"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/arrow-go/v18/arrow/scalar"
)

// MakeExecBatch binds partial input to schema, filling missing fields with
// nulls and casting fields whose types differ.
func MakeExecBatch(ctx context.Context, schema *arrow.Schema, partial compute.Datum) (out compute.ExecBatch, err error) {
	// cleanup if we get an error
	defer func() {
		if err != nil {
			for _, v := range out.Values {
				if v != nil {
					v.Release()
				}
			}
			out = compute.ExecBatch{}
		}
	}()

	if partial.Kind() == compute.KindRecord {
		partialBatch := partial.(*compute.RecordDatum).Value
		batchSchema := partialBatch.Schema()

		out.Values = make([]compute.Datum, schema.NumFields())
		out.Len = partialBatch.NumRows()

		for i, field := range schema.Fields() {
			idxes := batchSchema.FieldIndices(field.Name)
			switch len(idxes) {
			case 0:
				out.Values[i] = &compute.ScalarDatum{Value: scalar.MakeNullScalar(field.Type)}
			case 1:
				col := partialBatch.Column(idxes[0])
				if !arrow.TypeEqual(col.DataType(), field.Type) {
					// referenced field was present but didn't have expected type
					// we'll cast this case for now
					col, err = compute.CastArray(ctx, col, compute.SafeCastOptions(field.Type))
					if err != nil {
						return out, err
					}
					defer col.Release()
				}
				out.Values[i] = compute.NewDatum(col)
			default:
				err = fmt.Errorf("%w: exec batch field '%s' ambiguous, more than one match",
					arrow.ErrInvalid, field.Name)
				return out, err
			}
		}
		return
	}

	part, ok := partial.(compute.ArrayLikeDatum)
	if !ok {
		return out, fmt.Errorf("%w: MakeExecBatch from %s", arrow.ErrNotImplemented, partial)
	}

	// wasteful but useful for testing
	if part.Type().ID() == arrow.STRUCT {
		switch part := part.(type) {
		case *compute.ArrayDatum:
			arr := part.MakeArray().(*array.Struct)
			defer arr.Release()

			batch := array.RecordFromStructArray(arr, nil)
			defer batch.Release()
			return MakeExecBatch(ctx, schema, compute.NewDatumWithoutOwning(batch))
		case *compute.ScalarDatum:
			out.Len = 1
			out.Values = make([]compute.Datum, schema.NumFields())

			s := part.Value.(*scalar.Struct)
			dt := s.Type.(*arrow.StructType)

			for i, field := range schema.Fields() {
				idx, found := dt.FieldIdx(field.Name)
				if !found {
					out.Values[i] = &compute.ScalarDatum{Value: scalar.MakeNullScalar(field.Type)}
					continue
				}

				val := s.Value[idx]
				if !arrow.TypeEqual(val.DataType(), field.Type) {
					// referenced field was present but didn't have the expected
					// type. for now we'll cast this
					val, err = val.CastTo(field.Type)
					if err != nil {
						return out, err
					}
					if !s.Value[idx].IsValid() {
						// CastTo of a null returns a new scalar, which the datum owns
						out.Values[i] = &compute.ScalarDatum{Value: val}
						continue
					}
				}
				out.Values[i] = compute.NewDatum(val)
			}
			return
		}
	}

	return out, fmt.Errorf("%w: MakeExecBatch from %s", arrow.ErrNotImplemented, partial)
}

// IntervalYearToMonthDatum builds an interval_year extension scalar.
func IntervalYearToMonthDatum(mem memory.Allocator, years, months int32) (compute.Datum, error) {
	bldr := array.NewInt32Builder(mem)
	defer bldr.Release()

	bldr.Append(years)
	bldr.Append(months)
	arr := bldr.NewArray()
	defer arr.Release()
	return &compute.ScalarDatum{Value: scalar.NewExtensionScalar(
		scalar.NewFixedSizeListScalar(arr), IntervalYear())}, nil
}

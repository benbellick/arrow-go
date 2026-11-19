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
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/compute"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/arrow-go/v18/arrow/scalar"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/substrait-io/substrait-go/v8/expr"
	"github.com/substrait-io/substrait-go/v8/extensions"
	"github.com/substrait-io/substrait-go/v8/types"
)

func TestExecuteFieldRefNullStructSurvivesBatch(t *testing.T) {
	dictType := &arrow.DictionaryType{IndexType: arrow.PrimitiveTypes.Int32, ValueType: arrow.BinaryTypes.String}
	reg := NewExtensionIDRegistry()
	require.NoError(t, reg.RegisterType(extensions.ID{URN: "extension:test:dictionary", Name: "dict"}, dictType))
	extSet := NewExtensionSet(expr.NewEmptyExtensionRegistry(extensions.GetDefaultCollectionWithNoError()), reg)
	_, anchor, ok := extSet.EncodeType(dictType)
	require.True(t, ok)

	// FromSubstraitType names struct fields "1", "2", ...
	structType := arrow.StructOf(arrow.Field{Name: "1", Type: dictType, Nullable: true})
	schema := arrow.NewSchema([]arrow.Field{{Name: "d", Type: structType, Nullable: true}}, nil)
	ref, err := expr.NewRootFieldRef(expr.NewStructFieldRef(0), types.NewRecordTypeFromStruct(types.StructType{
		Nullability: types.NullabilityRequired,
		Types: []types.Type{&types.StructType{Nullability: types.NullabilityNullable,
			Types: []types.Type{&types.UserDefinedType{Nullability: types.NullabilityNullable, TypeReference: anchor}}}},
	}))
	require.NoError(t, err)

	tests := []struct {
		name    string
		partial compute.Datum
	}{
		{"missing record field", compute.NewDatumWithoutOwning(
			array.NewRecordBatch(arrow.NewSchema(nil, nil), nil, 1))},
		{"missing struct field", compute.NewDatumWithoutOwning(
			scalar.NewStructScalar(nil, arrow.StructOf()))},
		{"null struct field cast", compute.NewDatumWithoutOwning(scalar.NewStructScalar(
			[]scalar.Scalar{scalar.MakeNullScalar(arrow.BinaryTypes.String)},
			arrow.StructOf(arrow.Field{Name: "d", Type: arrow.BinaryTypes.String, Nullable: true})))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ExecuteScalarExpression(WithExtensionIDSet(context.Background(), extSet), schema, ref, tt.partial)
			require.NoError(t, err)
			dict := result.(*compute.ScalarDatum).Value.(*scalar.Struct).Value[0].(*scalar.Dictionary).Value.Dict
			data, _ := dict.Data().(*array.Data)
			require.NotNil(t, data, "result dictionary released by the batch")
			require.NotEmpty(t, data.Buffers())
			result.Release()
			assert.Nil(t, data.Buffers())
		})
	}
}

func TestLiteralToDatumIntervalYearToMonth(t *testing.T) {
	mem := memory.NewCheckedAllocator(memory.DefaultAllocator)
	defer mem.AssertSize(t, 0)

	extSet := NewExtensionSetDefault(
		expr.NewEmptyExtensionRegistry(extensions.GetDefaultCollectionWithNoError()))

	const (
		years  int32 = 3
		months int32 = 7
	)

	protoLitType := types.NewIntervalYearToMonthType()
	protoLit := &expr.ProtoLiteral{
		Value: &types.IntervalYearToMonth{Years: years, Months: months},
		Type:  &protoLitType,
	}
	expected, err := literalToDatum(mem, protoLit, extSet)
	require.NoError(t, err, "ProtoLiteral baseline failed")
	defer expected.Release()

	cases := []struct {
		name string
		lit  expr.Literal
	}{
		{"value", expr.IntervalYearToMonthLiteral{Years: years, Months: months}},
		{"pointer", &expr.IntervalYearToMonthLiteral{Years: years, Months: months}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := literalToDatum(mem, tc.lit, extSet)
			require.NoError(t, err)
			defer got.Release()
			assert.Truef(t, got.Equals(expected),
				"IntervalYearToMonthLiteral (%s) datum did not match ProtoLiteral baseline\nexpected: %s\ngot: %s",
				tc.name, expected, got)
		})
	}
}

func TestLiteralToDatumNullUserDefinedRelease(t *testing.T) {
	dictType := &arrow.DictionaryType{IndexType: arrow.PrimitiveTypes.Int32, ValueType: arrow.BinaryTypes.String}
	reg := NewExtensionIDRegistry()
	require.NoError(t, reg.RegisterType(extensions.ID{URN: "extension:test:dictionary", Name: "dict"}, dictType))
	extSet := NewExtensionSet(expr.NewEmptyExtensionRegistry(extensions.GetDefaultCollectionWithNoError()), reg)
	_, anchor, ok := extSet.EncodeType(dictType)
	require.True(t, ok)

	lit := expr.NewNullLiteral(&types.UserDefinedType{TypeReference: anchor})
	got, err := literalToDatum(memory.DefaultAllocator, lit, extSet)
	require.NoError(t, err)
	dict := got.(*compute.ScalarDatum).Value.(*scalar.Dictionary).Value.Dict.Data()
	require.NotEmpty(t, dict.Buffers())
	got.Release()
	assert.Nil(t, dict.Buffers())
}

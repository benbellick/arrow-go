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
	"strings"
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

func mustLiteral(t *testing.T) func(expr.Literal, error) expr.Literal {
	return func(lit expr.Literal, err error) expr.Literal {
		require.NoError(t, err)
		return lit
	}
}

func TestLiteralToV9(t *testing.T) {
	must := mustLiteral(t)
	literals := []expr.Literal{
		expr.NewNullLiteral(&types.Int32Type{Nullability: types.NullabilityNullable}),
		expr.NewPrimitiveLiteral(true, false),
		expr.NewPrimitiveLiteral(int8(1), false),
		expr.NewPrimitiveLiteral(int16(2), true),
		expr.NewPrimitiveLiteral(int32(3), false),
		expr.NewPrimitiveLiteral(int64(4), false),
		expr.NewPrimitiveLiteral(float32(1.5), false),
		expr.NewPrimitiveLiteral(2.5, false),
		expr.NewPrimitiveLiteral("hello", false),
		must(expr.NewLiteral(types.Timestamp(1000), false)),
		must(expr.NewLiteral(types.TimestampTz(1000), false)),
		must(expr.NewLiteral(types.Date(10), false)),
		must(expr.NewLiteral(types.Time(1000), false)),
		expr.NewFixedCharLiteral(types.FixedChar("abc"), false),
		expr.NewByteSliceLiteral([]byte("bytes"), false),
		expr.NewByteSliceLiteral(types.UUID(make([]byte, 16)), false),
		expr.NewFixedBinaryLiteral(types.FixedBinary("fixed"), false),
		must(expr.NewLiteral(&types.VarChar{Value: "varchar", Length: 10}, false)),
		must(expr.NewLiteral(&types.Decimal{Value: make([]byte, 16), Precision: 10, Scale: 2}, false)),
		must(expr.NewLiteral(&types.IntervalYearToMonth{Years: 1, Months: 2}, false)),
		expr.NewPrecisionTimestampLiteral(1000, types.PrecisionMicroSeconds, types.NullabilityRequired),
		expr.NewPrecisionTimestampTzLiteral(1000, types.PrecisionNanoSeconds, types.NullabilityNullable),
		must(expr.NewLiteral(expr.ListLiteralValue{
			expr.NewPrimitiveLiteral(int32(1), false),
			expr.NewPrimitiveLiteral(int32(2), false),
		}, false)),
		must(expr.NewLiteral(expr.StructLiteralValue{
			expr.NewPrimitiveLiteral("a", false),
			expr.NewPrimitiveLiteral(int64(1), true),
		}, false)),
		must(expr.NewLiteral(expr.MapLiteralValue{
			{Key: expr.NewPrimitiveLiteral("a", false), Value: expr.NewPrimitiveLiteral(int32(1), false)},
		}, false)),
	}

	for _, lit := range literals {
		t.Run(lit.String(), func(t *testing.T) {
			converted, err := newExprConverter().literal(lit)
			require.NoError(t, err)
			assert.Equal(t, lit.String(), converted.String())
		})
	}
}

func TestExecuteCustomV8FunctionMapping(t *testing.T) {
	mem := memory.NewCheckedAllocator(memory.DefaultAllocator)
	defer mem.AssertSize(t, 0)

	id := extensions.ID{URN: "extension:test:functions", Name: "plus_one"}
	reg := NewExtensionIDRegistry()
	var received *expr.ScalarFunction
	require.NoError(t, reg.AddSubstraitScalarToArrow(id,
		func(sf *expr.ScalarFunction, args []compute.Datum) (string, []compute.Datum, compute.FunctionOptions, error) {
			received = sf
			return "add", append(args, compute.NewDatum(scalar.NewInt32Scalar(1))), nil, nil
		}))

	set := NewExtensionSet(expr.NewEmptyExtensionRegistry(extensions.GetDefaultCollectionWithNoError()), reg)
	schema := arrow.NewSchema([]arrow.Field{{Name: "a", Type: arrow.PrimitiveTypes.Int32, Nullable: true}}, nil)
	ref, err := expr.NewRootFieldRef(expr.NewStructFieldRef(0), types.NewRecordTypeFromStruct(types.StructType{
		Nullability: types.NullabilityRequired,
		Types:       []types.Type{&types.Int32Type{Nullability: types.NullabilityNullable}},
	}))
	require.NoError(t, err)
	fn, err := expr.NewCustomScalarFunc(set.GetSubstraitRegistry(), extensions.NewScalarFuncVariant(id),
		&types.Int32Type{Nullability: types.NullabilityNullable}, nil, ref)
	require.NoError(t, err)

	rec, _, err := array.RecordFromJSON(mem, schema, strings.NewReader(`[{"a": 1}, {"a": null}]`))
	require.NoError(t, err)
	defer rec.Release()

	ctx := WithExtensionIDSet(compute.WithAllocator(context.Background(), mem), set)
	result, err := ExecuteScalarExpression(ctx, schema, fn, compute.NewDatumWithoutOwning(rec))
	require.NoError(t, err)
	defer result.Release()

	actual := result.(*compute.ArrayDatum).MakeArray()
	defer actual.Release()

	assert.Same(t, fn, received)
	assert.Equal(t, "[2 (null)]", actual.String())
}

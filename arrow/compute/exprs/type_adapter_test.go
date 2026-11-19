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
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/substrait-io/substrait-go/v8/types"
)

func TestV9TypeRoundTrip(t *testing.T) {
	const (
		nullable = types.NullabilityNullable
		required = types.NullabilityRequired
	)
	tests := []types.Type{
		&types.BooleanType{Nullability: nullable},
		&types.Int8Type{Nullability: required, TypeVariationRef: 3},
		&types.Int16Type{Nullability: nullable},
		&types.Int32Type{Nullability: nullable},
		&types.Int64Type{Nullability: types.NullabilityUnspecified},
		&types.Float32Type{Nullability: nullable},
		&types.Float64Type{Nullability: nullable},
		&types.StringType{Nullability: nullable},
		&types.BinaryType{Nullability: nullable},
		&types.DateType{Nullability: nullable},
		&types.IntervalYearType{Nullability: nullable},
		&types.UUIDType{Nullability: nullable},
		&types.FixedCharType{Nullability: nullable, Length: 5},
		&types.VarCharType{Nullability: nullable, Length: -1},
		&types.FixedBinaryType{Nullability: nullable, Length: 16},
		&types.DecimalType{Nullability: nullable, Precision: 38, Scale: 4},
		&types.PrecisionTimeType{Nullability: nullable, Precision: types.PrecisionNanoSeconds},
		&types.PrecisionTimestampType{Nullability: nullable, Precision: types.PrecisionMicroSeconds},
		&types.PrecisionTimestampTzType{PrecisionTimestampType: types.PrecisionTimestampType{
			Nullability: nullable, Precision: types.PrecisionSeconds}},
		&types.IntervalDayType{Nullability: nullable, Precision: types.PrecisionMicroSeconds},
		&types.StructType{Nullability: required, Types: []types.Type{
			&types.Int32Type{Nullability: nullable},
			&types.ListType{Nullability: nullable, Type: &types.StringType{Nullability: nullable}},
		}},
		&types.MapType{Nullability: nullable,
			Key:   &types.StringType{Nullability: required},
			Value: &types.Int64Type{Nullability: nullable}},
		&types.UserDefinedType{Nullability: nullable, TypeReference: 7},
	}

	for _, typ := range tests {
		t.Run(typ.String(), func(t *testing.T) {
			converted, err := toV9Type(typ)
			require.NoError(t, err)
			back, err := fromV9Type(converted)
			require.NoError(t, err)
			assert.True(t, typ.Equals(back), "got %s", back)
		})
	}
}

func TestV9TypeUnsupported(t *testing.T) {
	_, err := toV9Type(types.NewIntervalCompoundType())
	assert.ErrorIs(t, err, arrow.ErrNotImplemented)
}

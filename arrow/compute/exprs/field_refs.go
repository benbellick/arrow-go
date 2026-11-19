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

package exprs

import (
	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/compute"
	core "github.com/apache/arrow-go/v18/arrow/compute/exprs/v9"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/arrow-go/v18/arrow/scalar"
	"github.com/substrait-io/substrait-go/v8/expr"
)

// GetRefField evaluates the substrait field reference to retrieve the
// referenced field or return an error.
func GetRefField(ref expr.ReferenceSegment, fields []arrow.Field) (*arrow.Field, error) {
	converted, err := newExprConverter().segment(ref)
	if err != nil {
		return nil, err
	}
	return core.GetRefField(converted, fields)
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
	converted, err := newExprConverter().segment(ref)
	if err != nil {
		return nil, err
	}
	return core.GetScalar(converted, s, mem, coreExtensionSet(ext))
}

// GetReferencedValue retrieves the referenced (potentially nested) value from
// the provided datum which may be a scalar, array, or record batch. The
// result is a new reference that the caller must release.
func GetReferencedValue(mem memory.Allocator, ref expr.ReferenceSegment, value compute.Datum, ext ExtensionIDSet) (compute.Datum, error) {
	converted, err := newExprConverter().segment(ref)
	if err != nil {
		return nil, err
	}
	return core.GetReferencedValue(mem, converted, value, coreExtensionSet(ext))
}

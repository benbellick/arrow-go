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

import "github.com/apache/arrow-go/v18/arrow"

type OverflowBehavior string

const (
	OverflowSilent   OverflowBehavior = "SILENT"
	OverflowSaturate OverflowBehavior = "SATURATE"
	OverflowError    OverflowBehavior = "ERROR"
)

// EnumParser validates Substrait enum function option values.
type EnumParser[typ ~string] struct {
	values map[typ]struct{}
}

func (e *EnumParser[typ]) Parse(v string) (typ, error) {
	out := typ(v)
	if _, ok := e.values[out]; ok {
		return out, nil
	}
	return "", arrow.ErrNotFound
}

var OverflowParser = EnumParser[OverflowBehavior]{
	values: map[OverflowBehavior]struct{}{
		OverflowSilent:   {},
		OverflowSaturate: {},
		OverflowError:    {},
	},
}

type CaseSensitivity string

const (
	CaseSensitive        CaseSensitivity = "CASE_SENSITIVE"
	CaseInsensitive      CaseSensitivity = "CASE_INSENSITIVE"
	CaseInsensitiveASCII CaseSensitivity = "CASE_INSENSITIVE_ASCII"
)

var CaseSensitivityParser = EnumParser[CaseSensitivity]{
	values: map[CaseSensitivity]struct{}{
		CaseSensitive:        {},
		CaseInsensitive:      {},
		CaseInsensitiveASCII: {},
	},
}

// ParseOption returns the first implemented value in opts, or def if opts is
// empty.
func ParseOption[typ ~string](opts []string, parser *EnumParser[typ], implemented []typ, def typ) (typ, error) {
	if len(opts) == 0 {
		return def, nil
	}

	for _, o := range opts {
		p, err := parser.Parse(o)
		if err != nil {
			return def, arrow.ErrInvalid
		}
		for _, i := range implemented {
			if i == p {
				return p, nil
			}
		}
	}

	return def, arrow.ErrNotImplemented
}

var SubstraitToArrowFuncMap = map[string]string{
	"lt":  "less",
	"gt":  "greater",
	"lte": "less_equal",
	"gte": "greater_equal",
	"or":  "or_kleene",
	"and": "and_kleene",
}

var ArrowToSubstraitFuncMap = map[string]string{
	"less":          "lt",
	"greater":       "gt",
	"less_equal":    "lte",
	"greater_equal": "gte",
	"and_kleene":    "and",
	"or_kleene":     "or",
}

// Copyright 2023 The Mellium Contributors.
// Use of this source code is governed by the BSD 2-clause
// license that can be found in the LICENSE file.

package sce

func clear(a []byte) {
	for i := range a {
		(a)[i] = 0
	}
}

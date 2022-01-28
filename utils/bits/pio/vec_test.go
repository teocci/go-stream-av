// Package pio
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package pio

import (
	"fmt"
)

func ExampleVec() {
	vec := [][]byte{{1, 2, 3}, {4, 5, 6, 7, 8, 9}, {10, 11, 12, 13}}
	println(VecLen(vec))

	vec = VecSlice(vec, 1, -1)
	fmt.Println(vec)

	vec = VecSlice(vec, 2, -1)
	fmt.Println(vec)

	vec = VecSlice(vec, 8, 8)
	fmt.Println(vec)

	// Output:
	//[[2 3] [4 5 6 7 8 9] [10 11 12 13]]
	//[[4 5 6 7 8 9] [10 11 12 13]]
	//[]
}

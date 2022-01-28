// Package nvr
// Created by RTT.
// Author: teocci@yandex.com on 2021-Oct-27
package nvr

type DeMuxer struct {
}

//NewDeMuxer func
func NewDeMuxer() *DeMuxer {
	return &DeMuxer{}
}

//ReadIndex func
func (dm *DeMuxer) ReadIndex() (err error) {
	return nil
}

//ReadRange func
func (dm *DeMuxer) ReadRange() (err error) {
	return nil
}

//ReadGop func
func (dm *DeMuxer) ReadGop() (err error) {
	return nil
}

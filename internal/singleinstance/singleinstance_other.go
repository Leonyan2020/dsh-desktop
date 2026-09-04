//go:build !windows

package singleinstance

func Acquire() bool                      { return true }
func ListenShowWindow(callback func()) {}

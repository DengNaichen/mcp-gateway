//go:build !darwin

package main

func configureWindowChrome() func() { return func() {} }

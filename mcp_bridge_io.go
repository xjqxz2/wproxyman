package main

import (
	"io"
	"os"
)

// stdin / stdout 抽成函数，便于测试替换。
func stdin() io.Reader  { return os.Stdin }
func stdout() io.Writer { return os.Stdout }

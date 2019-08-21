package main

import (
	"strconv"
	"testing"
)

func TestStrconv(t *testing.T) {
	var i int64
	i = 123456789
	t.Run("123456789", func(t *testing.T) {
		s1 := strconv.FormatInt(i, 10)
		s2 := string(i)
		if s1 == s2 {
			t.Errorf("%s , %s", s1, s2)
		}
	})
}

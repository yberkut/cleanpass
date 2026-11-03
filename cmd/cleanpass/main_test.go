package main_test

import (
	"os"
	"testing"

	"github.com/yberkut/cleanpass/internal/testkit"
)

func TestMain(m *testing.M) { os.Exit(testkit.RunMain(m)) }

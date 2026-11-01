package cli

import (
	"os"
	"time"

	"github.com/yberkut/cleanpass/internal/report"
	"github.com/yberkut/cleanpass/internal/session"
)

func init() { register("compare", runCompare) }

// runCompare shows the passport of each copy. It only reads.
func runCompare(env Env, args []string) int {
	copies, problem := parseCopies(args)
	if problem != "" {
		return fail(env, problem)
	}
	if problem := checkCopies(copies); problem != "" {
		return fail(env, problem)
	}

	dialog := session.New(env.Stdin, env.Stderr)
	passports := make([]report.Passport, 0, len(copies))
	for _, c := range copies {
		snap, err := dialog.Open(c)
		if err != nil {
			return fail(env, describeFileError(c.Path, err))
		}
		info, err := os.Stat(c.Path)
		if err != nil {
			return fail(env, describeFileError(c.Path, err))
		}
		passports = append(passports, report.Passport{
			Path:       c.Path,
			Size:       info.Size(),
			FileDate:   info.ModTime(),
			LastChange: snap.LastChange(),
			Stats:      snap.Stats(),
			Format:     snap.Format,
		})
	}
	markFreshest(passports)
	if err := report.WritePassports(env.Stdout, passports); err != nil {
		return exitError
	}
	return exitOK
}

// markFreshest marks every copy with the newest change inside the database. Copies
// with the same newest time are all marked. One copy has nothing to compare with.
func markFreshest(passports []report.Passport) {
	if len(passports) < 2 {
		return
	}
	var newest time.Time
	for i := range passports {
		if passports[i].LastChange.After(newest) {
			newest = passports[i].LastChange
		}
	}
	for i := range passports {
		passports[i].Freshest = !newest.IsZero() && passports[i].LastChange.Equal(newest)
	}
}

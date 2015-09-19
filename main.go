// Benchwrap automates running and analysing Go benchmarks.
//
// Usage:
//
//	usage: benchwrap rev.old [rev.new] [rev.more ...]
//
// Benchwrap runs a set of benchmarks n times for one or more git revisions. It
// feeds the collected benchmark results to `benchstat`, which in turn analyzes
// the data and prints it to stdout. Each input rev must be a valid git commit
// or reference, e.g. a hash, tag or branch. Options to `go test` and
// `benchstat` can be given by using the appropriate flags.
//
// Options:
//
//	  -bench regexp
//	        regexp denoting benchmarks to run (go test -bench) (default ".")
//	  -delta-test test
//	        forward test to benchstat -delta-test flag
//	  -gt-flags string
//	        forward quoted string of flags to go test
//	  -h-vs-h1
// 		use HEAD~1 as rev.old and HEAD as rev.new
//	  -html
//	        invoke benchstat with -html flag
//	  -n number
//	        number of go test invocations per git revision (default 10)
//	  -pkgs string
//	        packages to test (go test [packages]) (default ".")
//	  -v    print verbose output to stderr
//
// Dependencies:
//
// 	go get [-u] rsc.io/benchstat
//
// Example
//
// In a git repository, run all `Foo` benchmarks 10 times each for git tag
// `v0.42`, commit `cdd48c8a` and branch master, and analyse results with
// benchstat:
//
// 	$ benchwrap -n 10 -bench=Foo v0.42 cdd48c8a master
//
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

var (
	bench   = flag.String("bench", ".", "`regexp` denoting benchmarks to run (go test -bench)")
	nflag   = flag.Int("n", 10, "`number` of go test invocations per git revision")
	hvsh1   = flag.Bool("h-vs-h1", false, "use HEAD~1 as rev.old and HEAD as rev.new")
	gtpkgs  = flag.String("pkgs", ".", "packages to test (go test [packages])")
	gtflags = flag.String("gt-flags", "", "forward quoted `string` of flags to go test")
	bsdelta = flag.String("delta-test", "", "forward `test` to benchstat -delta-test flag")
	bshtml  = flag.Bool("html", false, "invoke benchstat with -html flag")
	verbose = flag.Bool("v", false, "print verbose output to stderr")
)

func usage() {
	fmt.Fprintf(os.Stderr, "usage: benchwrap rev.old [rev.new] [rev.more ...]\n")
	fmt.Fprintf(os.Stderr, "\noptions:\n")
	flag.PrintDefaults()
	os.Exit(2)
}

type rev struct {
	bytes.Buffer
	name      string
	sha1      string
	sha1Short string
	fpath     string
}

func main() {
	flag.Usage = usage
	flag.Parse()
	if flag.NArg() < 1 && !*hvsh1 {
		flag.Usage()
	}

	_, err := exec.LookPath("benchstat")
	if err != nil {
		fmt.Fprint(os.Stderr, "no benchstat binary in $PATH\n")
		fmt.Fprint(os.Stderr, "go get [-u] rsc.io/benchstat\n")
		os.Exit(2)
	}

	setupLogging()

	var (
		args    []string
		revs    []*rev
		tmpdir  string
		nmaxlen int
		bsargs  []string
		bsout   []byte
		out     bytes.Buffer
	)

	currentRevName, err := gitNameRev("HEAD")
	if err != nil {
		goto err
	}

	if *hvsh1 {
		args = []string{"HEAD~1", "HEAD"}
	} else {
		args = flag.Args()
	}

	for _, arg := range args {
		r := &rev{}
		r.name = arg
		r.sha1, err = gitRevParseVerify(arg)
		if err != nil {
			goto err
		}
		r.sha1Short = shortSHA1(r.sha1)
		revs = append(revs, r)
	}

	for _, rev := range revs {
		err = gitCheckout(rev.sha1)
		if err != nil {
			goto err
		}
		for i := 0; i < *nflag; i++ {
			var tmp []byte
			var args []string
			args = append(
				args,
				"test",
				*gtpkgs,
				"-run=NONE",
				"-bench="+*bench,
			)
			if *gtflags != "" {
				fs := strings.Fields(*gtflags)
				args = append(args, fs...)
			}
			tmp, err = run("go", args...)
			if err != nil {
				goto err
			}
			log.Printf("%s\n", tmp)
			rev.Write(tmp)
		}
	}

	tmpdir, err = ioutil.TempDir("", "bw")
	if err != nil {
		goto err
	}

	for _, rev := range revs {
		rev.fpath = filepath.Join(tmpdir, rev.sha1Short)
		err = ioutil.WriteFile(
			rev.fpath,
			rev.Bytes(),
			0644,
		)
		if err != nil {
			goto err
		}
	}

	if *bshtml {
		bsargs = append(bsargs, "-html")
	}
	if *bsdelta != "" {
		bsargs = append(bsargs, "-delta-test", *bsdelta)
	}
	for _, rev := range revs {
		bsargs = append(bsargs, rev.fpath)
	}
	bsout, err = run(
		"benchstat",
		bsargs...,
	)
	if err != nil {
		goto err
	}

	for _, rev := range revs {
		n := utf8.RuneCountInString(rev.name)
		if n > nmaxlen {
			nmaxlen = n
		}
	}

	switch len(revs) {
	case 1:
		out.WriteString(fmt.Sprintf("%s: %s\n", revs[0].name, revs[0].sha1))
	case 2:
		out.WriteString(fmt.Sprintf("%s:\t%s\n", "old", revs[0].sha1))
		out.WriteString(fmt.Sprintf("%s:\t%s\n", "new", revs[1].sha1))
	default:
		for _, rev := range revs {
			out.WriteString(fmt.Sprintf("%*s", -nmaxlen, rev.name))
			out.WriteString(fmt.Sprintf("\t%s\n", rev.sha1))
		}
	}

	out.WriteByte('\n')
	out.Write(bsout)
	out.WriteByte('\n')

	os.Stdout.Write(out.Bytes())

	gitCheckout(currentRevName)
	if tmpdir != "" {
		err = os.RemoveAll(tmpdir)
		if err != nil {
			log.Println(err)
		}
	}
	os.Exit(0)

err:
	gitCheckout(currentRevName)
	if tmpdir != "" {
		os.RemoveAll(tmpdir)
	}
	fmt.Fprintf(os.Stderr, "benchwrap: %v\n", err)
	os.Exit(2)
}

func gitNameRev(rev string) (name string, err error) {
	out, err := run("git", "name-rev", "--name-only", rev)
	return string(out), err
}

func gitRevParseVerify(rev string) (sha1 string, err error) {
	out, err := run("git", "rev-parse", "--verify", rev)
	return string(out), err
}

func gitCheckout(sha1 string) error {
	_, err := run("git", "checkout", sha1)
	return err
}

func run(command string, args ...string) ([]byte, error) {
	log.Println(strings.Join(append([]string{command}, args...), " "))
	cmd := exec.Command(command, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("error: %v\n%s\n", err, out)
	}
	return bytes.TrimSuffix(out, []byte{'\n'}), nil
}

func shortSHA1(sha1 string) string {
	if len(sha1) < 5 {
		return sha1
	}
	return sha1[:5]
}

func setupLogging() {
	log.SetPrefix("benchwrap: ")
	log.SetFlags(0)
	if !*verbose {
		log.SetOutput(ioutil.Discard)
	}
}

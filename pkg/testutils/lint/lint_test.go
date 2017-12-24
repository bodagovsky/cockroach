// Copyright 2016 The Cockroach Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
// implied. See the License for the specific language governing
// permissions and limitations under the License.

// +build lint

package lint

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"go/types"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/cmd/urlcheck/lib/urlcheck"
	sqlparser "github.com/cockroachdb/cockroach/pkg/sql/parser"
	"github.com/ghemawat/stream"
	"github.com/kisielk/gotool"
	"github.com/pkg/errors"
	"golang.org/x/tools/go/buildutil"
	"golang.org/x/tools/go/loader"
	"honnef.co/go/tools/lint"
	"honnef.co/go/tools/simple"
	"honnef.co/go/tools/staticcheck"
	"honnef.co/go/tools/unused"
)

const cockroachDB = "github.com/cockroachdb/cockroach/pkg"

func dirCmd(
	dir string, name string, args ...string,
) (*exec.Cmd, *bytes.Buffer, stream.Filter, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stderr := new(bytes.Buffer)
	cmd.Stderr = stderr
	return cmd, stderr, stream.ReadLines(stdout), nil
}

func TestLint(t *testing.T) {
	pkg, err := build.Import(cockroachDB, "", build.FindOnly)
	if err != nil {
		t.Skip(err)
	}

	t.Run("TestCopyrightHeaders", func(t *testing.T) {
		t.Parallel()
		cmd, stderr, filter, err := dirCmd(pkg.Dir, "git", "grep", "-LE", `^// (Copyright|Code generated by)`, "--", "*.go")
		if err != nil {
			t.Fatal(err)
		}

		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		if err := stream.ForEach(filter, func(s string) {
			t.Errorf(`%s <- missing license header`, s)
		}); err != nil {
			t.Error(err)
		}

		if err := cmd.Wait(); err != nil {
			if out := stderr.String(); len(out) > 0 {
				t.Fatalf("err=%s, stderr=%s", err, out)
			}
		}
	})

	t.Run("TestMissingLeakTest", func(t *testing.T) {
		t.Parallel()
		cmd, stderr, filter, err := dirCmd(pkg.Dir, "util/leaktest/check-leaktest.sh")
		if err != nil {
			t.Fatal(err)
		}

		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		if err := stream.ForEach(filter, func(s string) {
			t.Error(s)
		}); err != nil {
			t.Error(err)
		}

		if err := cmd.Wait(); err != nil {
			if out := stderr.String(); len(out) > 0 {
				t.Fatalf("err=%s, stderr=%s", err, out)
			}
		}
	})

	t.Run("TestTabsInShellScripts", func(t *testing.T) {
		t.Parallel()
		cmd, stderr, filter, err := dirCmd(pkg.Dir, "git", "grep", "-nF", "\t", "--", "*.sh")
		if err != nil {
			t.Fatal(err)
		}

		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		if err := stream.ForEach(filter, func(s string) {
			t.Errorf(`%s <- tab detected, use spaces instead`, s)
		}); err != nil {
			t.Error(err)
		}

		if err := cmd.Wait(); err != nil {
			if out := stderr.String(); len(out) > 0 {
				t.Fatalf("err=%s, stderr=%s", err, out)
			}
		}
	})

	t.Run("TestEnvutil", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			re       string
			excludes []string
		}{
			{re: `\bos\.(Getenv|LookupEnv)\("COCKROACH`},
			{
				re: `\bos\.(Getenv|LookupEnv)\(`,
				excludes: []string{
					":!acceptance",
					":!ccl/acceptanceccl/backup_test.go",
					":!ccl/sqlccl/backup_cloud_test.go",
					":!ccl/storageccl/export_storage_test.go",
					":!cmd",
					":!testutils/lint",
					":!util/envutil/env.go",
					":!util/log/clog.go",
					":!util/color/color.go",
					":!util/sdnotify/sdnotify_unix.go",
				},
			},
		} {
			cmd, stderr, filter, err := dirCmd(
				pkg.Dir,
				"git",
				append([]string{
					"grep",
					"-nE",
					tc.re,
					"--",
					"*.go",
				}, tc.excludes...)...,
			)
			if err != nil {
				t.Fatal(err)
			}

			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}

			if err := stream.ForEach(filter, func(s string) {
				t.Errorf(`%s <- forbidden; use "envutil" instead`, s)
			}); err != nil {
				t.Error(err)
			}

			if err := cmd.Wait(); err != nil {
				if out := stderr.String(); len(out) > 0 {
					t.Fatalf("err=%s, stderr=%s", err, out)
				}
			}
		}
	})

	t.Run("TestSyncutil", func(t *testing.T) {
		t.Parallel()
		cmd, stderr, filter, err := dirCmd(
			pkg.Dir,
			"git",
			"grep",
			"-nE",
			`\bsync\.(RW)?Mutex`,
			"--",
			"*.go",
			":!util/syncutil/mutex_sync.go",
		)
		if err != nil {
			t.Fatal(err)
		}

		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		if err := stream.ForEach(filter, func(s string) {
			t.Errorf(`%s <- forbidden; use "syncutil.{,RW}Mutex" instead`, s)
		}); err != nil {
			t.Error(err)
		}

		if err := cmd.Wait(); err != nil {
			if out := stderr.String(); len(out) > 0 {
				t.Fatalf("err=%s, stderr=%s", err, out)
			}
		}
	})

	t.Run("TestTodoStyle", func(t *testing.T) {
		t.Parallel()
		// TODO(tamird): enforce presence of name.
		cmd, stderr, filter, err := dirCmd(pkg.Dir, "git", "grep", "-nE", `\sTODO\([^)]+\)[^:]`, "--", "*.go")
		if err != nil {
			t.Fatal(err)
		}

		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		if err := stream.ForEach(filter, func(s string) {
			t.Errorf(`%s <- use 'TODO(...): ' instead`, s)
		}); err != nil {
			t.Error(err)
		}

		if err := cmd.Wait(); err != nil {
			if out := stderr.String(); len(out) > 0 {
				t.Fatalf("err=%s, stderr=%s", err, out)
			}
		}
	})

	t.Run("TestNonZeroOffsetInTests", func(t *testing.T) {
		t.Parallel()
		cmd, stderr, filter, err := dirCmd(pkg.Dir, "git", "grep", "-nE", `hlc\.NewClock\([^)]+, 0\)`, "--", "*_test.go")
		if err != nil {
			t.Fatal(err)
		}

		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		if err := stream.ForEach(filter, func(s string) {
			t.Errorf(`%s <- use non-zero clock offset`, s)
		}); err != nil {
			t.Error(err)
		}

		if err := cmd.Wait(); err != nil {
			if out := stderr.String(); len(out) > 0 {
				t.Fatalf("err=%s, stderr=%s", err, out)
			}
		}
	})

	t.Run("TestTimeutil", func(t *testing.T) {
		t.Parallel()
		cmd, stderr, filter, err := dirCmd(
			pkg.Dir,
			"git",
			"grep",
			"-nE",
			`\btime\.(Now|Since|Unix)\(`,
			"--",
			"*.go",
			":!**/embedded.go",
			":!util/timeutil/time.go",
			":!util/timeutil/now_unix.go",
			":!util/timeutil/now_windows.go",
			":!util/tracing/tracer_span.go",
			":!util/tracing/tracer.go",
		)
		if err != nil {
			t.Fatal(err)
		}

		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		if err := stream.ForEach(filter, func(s string) {
			t.Errorf(`%s <- forbidden; use "timeutil" instead`, s)
		}); err != nil {
			t.Error(err)
		}

		if err := cmd.Wait(); err != nil {
			if out := stderr.String(); len(out) > 0 {
				t.Fatalf("err=%s, stderr=%s", err, out)
			}
		}
	})

	t.Run("TestGrpc", func(t *testing.T) {
		t.Parallel()
		cmd, stderr, filter, err := dirCmd(
			pkg.Dir,
			"git",
			"grep",
			"-nE",
			`\bgrpc\.NewServer\(`,
			"--",
			"*.go",
			":!rpc/context_test.go",
			":!rpc/context.go",
			":!util/grpcutil/grpc_util_test.go",
		)
		if err != nil {
			t.Fatal(err)
		}

		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		if err := stream.ForEach(filter, func(s string) {
			t.Errorf(`%s <- forbidden; use "rpc.NewServer" instead`, s)
		}); err != nil {
			t.Error(err)
		}

		if err := cmd.Wait(); err != nil {
			if out := stderr.String(); len(out) > 0 {
				t.Fatalf("err=%s, stderr=%s", err, out)
			}
		}
	})

	t.Run("TestPGErrors", func(t *testing.T) {
		t.Parallel()
		// TODO(justin): we should expand the packages this applies to as possible.
		cmd, stderr, filter, err := dirCmd(pkg.Dir, "git", "grep", "-nE", `((fmt|errors).Errorf|errors.New)`, "--", "sql/parser/*.go", "util/ipaddr/*.go")
		if err != nil {
			t.Fatal(err)
		}

		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		if err := stream.ForEach(filter, func(s string) {
			t.Errorf(`%s <- forbidden; use "pgerror.NewErrorf" instead`, s)
		}); err != nil {
			t.Error(err)
		}

		if err := cmd.Wait(); err != nil {
			if out := stderr.String(); len(out) > 0 {
				t.Fatalf("err=%s, stderr=%s", err, out)
			}
		}
	})

	t.Run("TestProtoClone", func(t *testing.T) {
		t.Parallel()
		cmd, stderr, filter, err := dirCmd(
			pkg.Dir,
			"git",
			"grep",
			"-nE",
			`\.Clone\([^)]`,
			"--",
			"*.go",
			":!util/protoutil/clone_test.go",
			":!util/protoutil/clone.go",
		)
		if err != nil {
			t.Fatal(err)
		}

		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		if err := stream.ForEach(stream.Sequence(
			filter,
			stream.GrepNot(`protoutil\.Clone\(`),
		), func(s string) {
			t.Errorf(`%s <- forbidden; use "protoutil.Clone" instead`, s)
		}); err != nil {
			t.Error(err)
		}

		if err := cmd.Wait(); err != nil {
			if out := stderr.String(); len(out) > 0 {
				t.Fatalf("err=%s, stderr=%s", err, out)
			}
		}
	})

	t.Run("TestProtoMarshal", func(t *testing.T) {
		t.Parallel()
		cmd, stderr, filter, err := dirCmd(
			pkg.Dir,
			"git",
			"grep",
			"-nE",
			`\.Marshal\(`,
			"--",
			"*.go",
			":!util/protoutil/marshal.go",
			":!util/protoutil/marshaler.go",
			":!settings/settings_test.go",
		)
		if err != nil {
			t.Fatal(err)
		}

		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		if err := stream.ForEach(stream.Sequence(
			filter,
			stream.GrepNot(`(json|yaml|protoutil|\.Field)\.Marshal\(`),
		), func(s string) {
			t.Errorf(`%s <- forbidden; use "protoutil.Marshal" instead`, s)
		}); err != nil {
			t.Error(err)
		}

		if err := cmd.Wait(); err != nil {
			if out := stderr.String(); len(out) > 0 {
				t.Fatalf("err=%s, stderr=%s", err, out)
			}
		}
	})

	t.Run("TestProtoUnmarshal", func(t *testing.T) {
		t.Parallel()
		cmd, stderr, filter, err := dirCmd(
			pkg.Dir,
			"git",
			"grep",
			"-nE",
			`\.Unmarshal\(`,
			"--",
			"*.go",
			":!*.pb.go",
			":!util/protoutil/marshal.go",
			":!util/protoutil/marshaler.go",
		)
		if err != nil {
			t.Fatal(err)
		}

		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		if err := stream.ForEach(stream.Sequence(
			filter,
			stream.GrepNot(`(json|jsonpb|yaml|protoutil)\.Unmarshal\(`),
		), func(s string) {
			t.Errorf(`%s <- forbidden; use "protoutil.Unmarshal" instead`, s)
		}); err != nil {
			t.Error(err)
		}

		if err := cmd.Wait(); err != nil {
			if out := stderr.String(); len(out) > 0 {
				t.Fatalf("err=%s, stderr=%s", err, out)
			}
		}
	})

	t.Run("TestProtoMessage", func(t *testing.T) {
		t.Parallel()
		cmd, stderr, filter, err := dirCmd(
			pkg.Dir,
			"git",
			"grep",
			"-nEw",
			`proto\.Message`,
			"--",
			"*.go",
			":!*.pb.go",
			":!*.pb.gw.go",
			":!util/protoutil/jsonpb_marshal.go",
			":!util/protoutil/marshal.go",
			":!util/protoutil/marshaler.go",
		)
		if err != nil {
			t.Fatal(err)
		}

		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		if err := stream.ForEach(stream.Sequence(
			filter,
		), func(s string) {
			t.Errorf(`%s <- forbidden; use "protoutil.Message" instead`, s)
		}); err != nil {
			t.Error(err)
		}

		if err := cmd.Wait(); err != nil {
			if out := stderr.String(); len(out) > 0 {
				t.Fatalf("err=%s, stderr=%s", err, out)
			}
		}
	})

	t.Run("TestYaml", func(t *testing.T) {
		t.Parallel()
		cmd, stderr, filter, err := dirCmd(pkg.Dir, "git", "grep", "-nE", `\byaml\.Unmarshal\(`, "--", "*.go")
		if err != nil {
			t.Fatal(err)
		}

		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		if err := stream.ForEach(filter, func(s string) {
			t.Errorf(`%s <- forbidden; use "yaml.UnmarshalStrict" instead`, s)
		}); err != nil {
			t.Error(err)
		}

		if err := cmd.Wait(); err != nil {
			if out := stderr.String(); len(out) > 0 {
				t.Fatalf("err=%s, stderr=%s", err, out)
			}
		}
	})

	t.Run("TestImportNames", func(t *testing.T) {
		t.Parallel()
		cmd, stderr, filter, err := dirCmd(pkg.Dir, "git", "grep", "-nE", `^(import|\s+)(\w+ )?"database/sql"$`, "--", "*.go")
		if err != nil {
			t.Fatal(err)
		}

		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		if err := stream.ForEach(stream.Sequence(
			filter,
			stream.GrepNot(`gosql "database/sql"`),
		), func(s string) {
			t.Errorf(`%s <- forbidden; import "database/sql" as "gosql" to avoid confusion with "cockroach/sql"`, s)
		}); err != nil {
			t.Error(err)
		}

		if err := cmd.Wait(); err != nil {
			if out := stderr.String(); len(out) > 0 {
				t.Fatalf("err=%s, stderr=%s", err, out)
			}
		}
	})

	t.Run("TestMisspell", func(t *testing.T) {
		t.Parallel()
		cmd, stderr, filter, err := dirCmd(pkg.Dir, "git", "ls-files")
		if err != nil {
			t.Fatal(err)
		}

		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		ignoredRules := []string{
			"licence",
			"analyse", // required by SQL grammar
		}

		if err := stream.ForEach(stream.Sequence(
			filter,
			stream.GrepNot(`.*\.lock`),
			stream.Map(func(s string) string {
				return filepath.Join(pkg.Dir, s)
			}),
			stream.Xargs("misspell", "-locale", "US", "-i", strings.Join(ignoredRules, ",")),
		), func(s string) {
			t.Errorf(s)
		}); err != nil {
			t.Error(err)
		}

		if err := cmd.Wait(); err != nil {
			if out := stderr.String(); len(out) > 0 {
				t.Fatalf("err=%s, stderr=%s", err, out)
			}
		}
	})

	t.Run("TestGofmtSimplify", func(t *testing.T) {
		t.Parallel()
		cmd, stderr, filter, err := dirCmd(pkg.Dir, "gofmt", "-s", "-d", "-l", ".")
		if err != nil {
			t.Fatal(err)
		}

		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		if err := stream.ForEach(filter, func(s string) {
			t.Error(s)
		}); err != nil {
			t.Error(err)
		}

		if err := cmd.Wait(); err != nil {
			if out := stderr.String(); len(out) > 0 {
				t.Fatalf("err=%s, stderr=%s", err, out)
			}
		}
	})

	t.Run("TestCrlfmt", func(t *testing.T) {
		t.Parallel()
		cmd, stderr, filter, err := dirCmd(pkg.Dir, "crlfmt", "-ignore", `\.pb(\.gw)?\.go`, "-tab", "2", ".")
		if err != nil {
			t.Fatal(err)
		}

		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		if err := stream.ForEach(filter, func(s string) {
			t.Error(s)
		}); err != nil {
			t.Error(err)
		}

		if err := cmd.Wait(); err != nil {
			if out := stderr.String(); len(out) > 0 {
				t.Fatalf("err=%s, stderr=%s", err, out)
			}
		}

		if t.Failed() {
			args := append([]string(nil), cmd.Args[1:len(cmd.Args)-1]...)
			args = append(args, "-w", pkg.Dir)
			for i := range args {
				args[i] = strconv.Quote(args[i])
			}
			t.Logf("run the following to fix your formatting:\n"+
				"\n%s %s\n\n"+
				"Don't forget to add amend the result to the correct commits.",
				cmd.Args[0], strings.Join(args, " "),
			)
		}
	})

	t.Run("TestVet", func(t *testing.T) {
		t.Parallel()
		// `go tool vet` is a special snowflake that emits all its output on
		// `stderr.
		cmd := exec.Command("go", "tool", "vet", "-source", "-all", "-shadow", "-printfuncs",
			strings.Join([]string{
				"Info:1",
				"Infof:1",
				"InfofDepth:2",
				"Warning:1",
				"Warningf:1",
				"WarningfDepth:2",
				"Error:1",
				"Errorf:1",
				"ErrorfDepth:2",
				"Fatal:1",
				"Fatalf:1",
				"FatalfDepth:2",
				"Event:1",
				"Eventf:1",
				"ErrEvent:1",
				"ErrEventf:1",
				"NewError:1",
				"NewErrorf:1",
				"VEvent:2",
				"VEventf:2",
				"UnimplementedWithIssueErrorf:1",
				"Wrapf:1",
			}, ","),
			".",
		)
		cmd.Dir = pkg.Dir
		var b bytes.Buffer
		cmd.Stdout = &b
		cmd.Stderr = &b
		switch err := cmd.Run(); err.(type) {
		case nil:
		case *exec.ExitError:
			// Non-zero exit is expected.
		default:
			t.Fatal(err)
		}

		if err := stream.ForEach(stream.Sequence(
			stream.FilterFunc(func(arg stream.Arg) error {
				scanner := bufio.NewScanner(&b)
				for scanner.Scan() {
					arg.Out <- scanner.Text()
				}
				return scanner.Err()
			}),
			stream.GrepNot(`declaration of "?(pE|e)rr"? shadows`),
			stream.GrepNot(`\.pb\.gw\.go:[0-9]+: declaration of "?ctx"? shadows`),
		), func(s string) {
			t.Error(s)
		}); err != nil {
			t.Error(err)
		}
	})

	t.Run("TestAuthorTags", func(t *testing.T) {
		t.Parallel()
		cmd, stderr, filter, err := dirCmd(pkg.Dir, "git", "grep", "-lE", "^// Author:")
		if err != nil {
			t.Fatal(err)
		}

		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		if err := stream.ForEach(filter, func(s string) {
			t.Errorf(`%s <- please remove the Author comment within`, s)
		}); err != nil {
			t.Error(err)
		}

		if err := cmd.Wait(); err != nil {
			if out := stderr.String(); len(out) > 0 {
				t.Fatalf("err=%s, stderr=%s", err, out)
			}
		}
	})

	t.Run("TestForbiddenImports", func(t *testing.T) {
		t.Parallel()

		// forbiddenImportPkg -> permittedReplacementPkg
		forbiddenImports := map[string]string{
			"golang.org/x/net/context": "context",
			"log":  "util/log",
			"path": "path/filepath",
			"github.com/golang/protobuf/proto": "github.com/gogo/protobuf/proto",
			"github.com/satori/go.uuid":        "util/uuid",
			"golang.org/x/sync/singleflight":   "github.com/cockroachdb/cockroach/pkg/util/syncutil/singleflight",
		}

		// grepBuf creates a grep string that matches any forbidden import pkgs.
		var grepBuf bytes.Buffer
		grepBuf.WriteByte('(')
		for forbiddenPkg := range forbiddenImports {
			grepBuf.WriteByte('|')
			grepBuf.WriteString(regexp.QuoteMeta(forbiddenPkg))
		}
		grepBuf.WriteString(")$")

		filter := stream.FilterFunc(func(arg stream.Arg) error {
			for _, useAllFiles := range []bool{false, true} {
				buildContext := build.Default
				buildContext.CgoEnabled = true
				buildContext.UseAllFiles = useAllFiles
			outer:
				for path := range buildutil.ExpandPatterns(&buildContext, []string{cockroachDB + "/..."}) {
					importPkg, err := buildContext.Import(path, pkg.Dir, 0)
					switch err.(type) {
					case nil:
						for _, s := range importPkg.Imports {
							arg.Out <- importPkg.ImportPath + ": " + s
						}
						for _, s := range importPkg.TestImports {
							arg.Out <- importPkg.ImportPath + ": " + s
						}
						for _, s := range importPkg.XTestImports {
							arg.Out <- importPkg.ImportPath + ": " + s
						}
					case *build.NoGoError:
					case *build.MultiplePackageError:
						if useAllFiles {
							continue outer
						}
					default:
						return errors.Wrapf(err, "error loading package %s", path)
					}
				}
			}
			return nil
		})
		settingsPkgPrefix := `github.com/cockroachdb/cockroach/pkg/settings`
		if err := stream.ForEach(stream.Sequence(
			filter,
			stream.Sort(),
			stream.Uniq(),
			stream.Grep(`^`+settingsPkgPrefix+`: | `+grepBuf.String()),
			stream.GrepNot(`cockroach/pkg/cmd/`),
			stream.GrepNot(`cockroach/pkg/testutils/lint: log$`),
			stream.GrepNot(`cockroach/pkg/(cli|security): syscall$`),
			stream.GrepNot(`cockroach/pkg/(base|security|util/(log|randutil|stop)): log$`),
			stream.GrepNot(`cockroach/pkg/(server/serverpb|ts/tspb): github\.com/golang/protobuf/proto$`),
			stream.GrepNot(`cockroach/pkg/util/caller: path$`),
			stream.GrepNot(`cockroach/pkg/ccl/storageccl: path$`),
			stream.GrepNot(`cockroach/pkg/util/uuid: github\.com/satori/go\.uuid$`),
		), func(s string) {
			pkgStr := strings.Split(s, ": ")
			importingPkg, importedPkg := pkgStr[0], pkgStr[1]

			// Test that a disallowed package is not imported.
			if replPkg, ok := forbiddenImports[importedPkg]; ok {
				t.Errorf(`%s <- please use %q instead of %q`, s, replPkg, importedPkg)
			}

			// Test that the settings package does not import CRDB dependencies.
			if importingPkg == settingsPkgPrefix && strings.HasPrefix(importedPkg, cockroachDB) {
				switch {
				case strings.HasSuffix(s, "humanizeutil"):
				case strings.HasSuffix(s, "protoutil"):
				case strings.HasSuffix(s, "testutils"):
				case strings.HasSuffix(s, settingsPkgPrefix):
				default:
					t.Errorf("%s <- please don't add CRDB dependencies to settings pkg", s)
				}
			}
		}); err != nil {
			t.Error(err)
		}
	})

	// Things that are packaged scoped are below here.
	pkgScope, ok := os.LookupEnv("PKG")
	if !ok {
		pkgScope = "./..."
	}

	// TODO(tamird): replace this with errcheck.NewChecker() when
	// https://github.com/dominikh/go-tools/issues/57 is fixed.
	t.Run("TestErrCheck", func(t *testing.T) {
		if testing.Short() {
			t.Skip("short flag")
		}
		excludesPath, err := filepath.Abs(filepath.Join("testdata", "errcheck_excludes.txt"))
		if err != nil {
			t.Fatal(err)
		}
		// errcheck uses 1GB of ram (as of 2017-02-18), so don't parallelize it.
		cmd, stderr, filter, err := dirCmd(
			pkg.Dir,
			"errcheck",
			"-exclude",
			excludesPath,
			pkgScope,
		)
		if err != nil {
			t.Fatal(err)
		}

		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		if err := stream.ForEach(filter, func(s string) {
			t.Errorf(`%s <- unchecked error`, s)
		}); err != nil {
			t.Error(err)
		}

		if err := cmd.Wait(); err != nil {
			if out := stderr.String(); len(out) > 0 {
				t.Fatalf("err=%s, stderr=%s", err, out)
			}
		}
	})

	t.Run("TestReturnCheck", func(t *testing.T) {
		if testing.Short() {
			t.Skip("short flag")
		}
		// returncheck uses 1GB of ram (as of 2017-02-18), so don't parallelize it.
		cmd, stderr, filter, err := dirCmd(pkg.Dir, "returncheck", pkgScope)
		if err != nil {
			t.Fatal(err)
		}

		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		if err := stream.ForEach(filter, func(s string) {
			t.Errorf(`%s <- unchecked error`, s)
		}); err != nil {
			t.Error(err)
		}

		if err := cmd.Wait(); err != nil {
			if out := stderr.String(); len(out) > 0 {
				t.Fatalf("err=%s, stderr=%s", err, out)
			}
		}
	})

	t.Run("TestGolint", func(t *testing.T) {
		t.Parallel()
		cmd, stderr, filter, err := dirCmd(pkg.Dir, "golint", pkgScope)
		if err != nil {
			t.Fatal(err)
		}

		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		if err := stream.ForEach(filter, func(s string) {
			t.Error(s)
		}); err != nil {
			t.Error(err)
		}

		if err := cmd.Wait(); err != nil {
			if out := stderr.String(); len(out) > 0 {
				t.Fatalf("err=%s, stderr=%s", err, out)
			}
		}
	})

	t.Run("TestMegacheck", func(t *testing.T) {
		if testing.Short() {
			t.Skip("short flag")
		}
		t.Parallel()

		ctx := gotool.DefaultContext
		releaseTags := ctx.BuildContext.ReleaseTags
		lastTag := releaseTags[len(releaseTags)-1]
		dotIdx := strings.IndexByte(lastTag, '.')
		goVersion, err := strconv.Atoi(lastTag[dotIdx+1:])
		if err != nil {
			t.Fatal(err)
		}
		// NB: this doesn't use `pkgScope` because `honnef.co/go/unused`
		// produces many false positives unless it inspects all our packages.
		paths := ctx.ImportPaths([]string{cockroachDB + "/..."})
		conf := loader.Config{
			Build:      &ctx.BuildContext,
			ParserMode: parser.ParseComments,
			ImportPkgs: make(map[string]bool, len(paths)),
		}
		for _, path := range paths {
			conf.ImportPkgs[path] = true
		}
		lprog, err := conf.Load()
		if err != nil {
			t.Fatal(err)
		}

		unusedChecker := unused.NewChecker(unused.CheckAll)
		unusedChecker.WholeProgram = true

		for checker, ignores := range map[lint.Checker][]lint.Ignore{
			&miscChecker{}:  nil,
			&timerChecker{}: nil,
			simple.NewChecker(): {
				&lint.GlobIgnore{Pattern: "github.com/cockroachdb/cockroach/pkg/security/securitytest/embedded.go", Checks: []string{"S1013"}},
				&lint.GlobIgnore{Pattern: "github.com/cockroachdb/cockroach/pkg/ui/embedded.go", Checks: []string{"S1013"}},
			},
			staticcheck.NewChecker(): {
				// sql/ir/irgen/parser/yaccpar:362:3: this value of irgenDollar is never used (SA4006)
				&lint.GlobIgnore{Pattern: "github.com/cockroachdb/cockroach/pkg/sql/ir/irgen/parser/irgen.go", Checks: []string{"SA4006"}},
				// The generated parser is full of `case` arms such as:
				//
				// case 1:
				// 	sqlDollar = sqlS[sqlpt-1 : sqlpt+1]
				// 	//line sql.y:781
				// 	{
				// 		sqllex.(*Scanner).stmts = sqlDollar[1].union.stmts()
				// 	}
				//
				// where the code in braces is generated from the grammar action; if
				// the action does not make use of the matched expression, sqlDollar
				// will be assigned but not used. This is expected and intentional.
				//
				// Concretely, the grammar:
				//
				// stmt:
				//   alter_table_stmt
				// | backup_stmt
				// | copy_from_stmt
				// | create_stmt
				// | delete_stmt
				// | drop_stmt
				// | explain_stmt
				// | help_stmt
				// | prepare_stmt
				// | execute_stmt
				// | deallocate_stmt
				// | grant_stmt
				// | insert_stmt
				// | rename_stmt
				// | revoke_stmt
				// | savepoint_stmt
				// | select_stmt
				//   {
				//     $$.val = $1.slct()
				//   }
				// | set_stmt
				// | show_stmt
				// | split_stmt
				// | transaction_stmt
				// | release_stmt
				// | truncate_stmt
				// | update_stmt
				// | /* EMPTY */
				//   {
				//     $$.val = Statement(nil)
				//   }
				//
				// is compiled into the `case` arm:
				//
				// case 28:
				// 	sqlDollar = sqlS[sqlpt-0 : sqlpt+1]
				// 	//line sql.y:830
				// 	{
				// 		sqlVAL.union.val = Statement(nil)
				// 	}
				//
				// which results in the unused warning:
				//
				// sql/parser/yaccpar:362:3: this value of sqlDollar is never used (SA4006)
				&lint.GlobIgnore{Pattern: "github.com/cockroachdb/cockroach/pkg/sql/parser/sql.go", Checks: []string{"SA4006"}},
			},
			unused.NewLintChecker(unusedChecker): {
				// sql/parser/yaccpar:14:6: type sqlParser is unused (U1000)
				// sql/parser/yaccpar:15:2: func sqlParser.Parse is unused (U1000)
				// sql/parser/yaccpar:16:2: func sqlParser.Lookahead is unused (U1000)
				// sql/parser/yaccpar:29:6: func sqlNewParser is unused (U1000)
				// sql/parser/yaccpar:152:6: func sqlParse is unused (U1000)
				&lint.GlobIgnore{Pattern: "github.com/cockroachdb/cockroach/pkg/sql/parser/sql.go", Checks: []string{"U1000"}},
				// Generated file containing many unused postgres error codes.
				&lint.GlobIgnore{Pattern: "github.com/cockroachdb/cockroach/pkg/sql/pgwire/pgerror/codes.go", Checks: []string{"U1000"}},
				// IR templates.
				&lint.GlobIgnore{Pattern: "github.com/cockroachdb/cockroach/pkg/sql/ir/base/*.go", Checks: []string{"U1000"}},
			},
		} {
			t.Run(checker.Name(), func(t *testing.T) {
				linter := lint.Linter{
					Checker:   checker,
					Ignores:   ignores,
					GoVersion: goVersion,
				}
				for _, p := range linter.Lint(lprog) {
					t.Errorf("%s: %s", p.Position, p)
				}
			})
		}
	})

	// TestLogicTestsLint verifies that all the logic test files start with
	// a LogicTest directive.
	t.Run("TestLogicTestsLint", func(t *testing.T) {
		t.Parallel()
		cmd, stderr, filter, err := dirCmd(
			pkg.Dir, "git", "ls-files", "sql/logictest/testdata/logic_test/",
		)
		if err != nil {
			t.Fatal(err)
		}

		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		if err := stream.ForEach(filter, func(filename string) {
			file, err := os.Open(filepath.Join(pkg.Dir, filename))
			if err != nil {
				t.Error(err)
				return
			}
			firstLine, err := bufio.NewReader(file).ReadString('\n')
			if err != nil {
				t.Errorf("reading first line of %s: %s", filename, err)
				return
			}
			if !strings.HasPrefix(firstLine, "# LogicTest:") {
				t.Errorf("%s must start with a directive, e.g. `# LogicTest: default`", filename)
			}
		}); err != nil {
			t.Error(err)
		}

		if err := cmd.Wait(); err != nil {
			if out := stderr.String(); len(out) > 0 {
				t.Fatalf("err=%s, stderr=%s", err, out)
			}
		}
	})

	// TestHelpURLs checks that all help texts have a valid documentation URL.
	t.Run("TestHelpURLs", func(t *testing.T) {
		if testing.Short() {
			t.Skip("short flag")
		}

		t.Parallel()
		var buf bytes.Buffer
		for key, body := range sqlparser.HelpMessages {
			msg := sqlparser.HelpMessage{Command: key, HelpMessageBody: body}
			buf.WriteString(msg.String())
		}
		cmd := exec.Command("grep", "-nE", urlcheck.URLRE)
		cmd.Stdin = &buf
		if err := urlcheck.CheckURLsFromGrepOutput(cmd); err != nil {
			t.Fatal(err)
		}
	})
}

type miscChecker struct{}

func (*miscChecker) Name() string {
	return "misccheck"
}

func (*miscChecker) Prefix() string {
	return "CR"
}

func (*miscChecker) Init(*lint.Program) {}

func (*miscChecker) Funcs() map[string]lint.Func {
	return map[string]lint.Func{
		"FloatToUnsigned": checkConvertFloatToUnsigned,
		"Unconvert":       checkUnconvert,
	}
}

func forAllFiles(j *lint.Job, fn func(node ast.Node) bool) {
	for _, f := range j.Program.Files {
		if !lint.IsGenerated(f) {
			ast.Inspect(f, fn)
		}
	}
}

// @ianlancetaylor via golang-nuts[0]:
//
// For the record, the spec says, in https://golang.org/ref/spec#Conversions:
// "In all non-constant conversions involving floating-point or complex
// values, if the result type cannot represent the value the conversion
// succeeds but the result value is implementation-dependent."  That is the
// case that applies here: you are converting a negative floating point number
// to uint64, which can not represent a negative value, so the result is
// implementation-dependent.  The conversion to int64 works, of course. And
// the conversion to int64 and then to uint64 succeeds in converting to int64,
// and when converting to uint64 follows a different rule: "When converting
// between integer types, if the value is a signed integer, it is sign
// extended to implicit infinite precision; otherwise it is zero extended. It
// is then truncated to fit in the result type's size."
//
// So, basically, don't convert a negative floating point number to an
// unsigned integer type.
//
// [0] https://groups.google.com/d/msg/golang-nuts/LH2AO1GAIZE/PyygYRwLAwAJ
//
// TODO(tamird): upstream this.
func checkConvertFloatToUnsigned(j *lint.Job) {
	forAllFiles(j, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		castType, ok := j.Program.Info.TypeOf(call.Fun).(*types.Basic)
		if !ok {
			return true
		}
		if castType.Info()&types.IsUnsigned == 0 {
			return true
		}
		for _, arg := range call.Args {
			argType, ok := j.Program.Info.TypeOf(arg).(*types.Basic)
			if !ok {
				continue
			}
			if argType.Info()&types.IsFloat == 0 {
				continue
			}
			j.Errorf(arg, "do not convert a floating point number to an unsigned integer type")
		}
		return true
	})
}

func walkForStmts(n ast.Node, fn func(ast.Stmt) bool) bool {
	fr, ok := n.(*ast.ForStmt)
	if !ok {
		return true
	}
	return walkStmts(fr.Body.List, fn)
}

func walkSelectStmts(n ast.Node, fn func(ast.Stmt) bool) bool {
	sel, ok := n.(*ast.SelectStmt)
	if !ok {
		return true
	}
	return walkStmts(sel.Body.List, fn)
}

func walkStmts(stmts []ast.Stmt, fn func(ast.Stmt) bool) bool {
	for _, stmt := range stmts {
		if !fn(stmt) {
			return false
		}
	}
	return true
}

// timerChecker assures that timeutil.Timer objects are used correctly, to
// avoid race conditions and deadlocks. These timers require callers to set
// their Read field to true when their channel has been received on. If this
// field is not set and the timer's Reset method is called, we will deadlock.
// This lint assures that the Read field is set in the most common case where
// Reset is used, within a for-loop where each iteration blocks on a select
// statement. The timers are usually used as timeouts on these select
// statements, and need to be reset after each iteration.
//
// for {
//   timer.Reset(...)
//   select {
//     case <-timer.C:
//       timer.Read = true   <--  lint verifies that this line is present
//     case ...:
//   }
// }
//
type timerChecker struct {
	timerType types.Type
}

func (*timerChecker) Name() string {
	return "timercheck"
}

func (*timerChecker) Prefix() string {
	return "T"
}

const timerChanName = "C"

func (m *timerChecker) Init(program *lint.Program) {
	timeutilPkg := program.Prog.Package("github.com/cockroachdb/cockroach/pkg/util/timeutil")
	if timeutilPkg == nil {
		log.Fatal("timeutil package not found")
	}
	timerObject := timeutilPkg.Pkg.Scope().Lookup("Timer")
	if timerObject == nil {
		log.Fatal("timeutil.Timer type not found")
	}
	m.timerType = timerObject.Type()

	func() {
		if typ, ok := m.timerType.Underlying().(*types.Struct); ok {
			for i := 0; i < typ.NumFields(); i++ {
				if typ.Field(i).Name() == timerChanName {
					return
				}
			}
		}
		log.Fatalf("no field called %q in type %s", timerChanName, m.timerType)
	}()
}

func (m *timerChecker) Funcs() map[string]lint.Func {
	return map[string]lint.Func{
		"TimeutilTimerRead": m.checkSetTimeutilTimerRead,
	}
}

func (m *timerChecker) selectorIsTimer(s *ast.SelectorExpr, info *types.Info) bool {
	selTyp := info.TypeOf(s.X)
	if ptr, ok := selTyp.(*types.Pointer); ok {
		selTyp = ptr.Elem()
	}
	return selTyp == m.timerType
}

func (m *timerChecker) checkSetTimeutilTimerRead(j *lint.Job) {
	forAllFiles(j, func(n ast.Node) bool {
		return walkForStmts(n, func(s ast.Stmt) bool {
			return walkSelectStmts(s, func(s ast.Stmt) bool {
				comm, ok := s.(*ast.CommClause)
				if !ok || comm.Comm == nil /* default: */ {
					return true
				}

				// if receiving on a timer's C chan.
				var unary ast.Expr
				switch v := comm.Comm.(type) {
				case *ast.AssignStmt:
					// case `now := <-timer.C:`
					unary = v.Rhs[0]
				case *ast.ExprStmt:
					// case `<-timer.C:`
					unary = v.X
				default:
					return true
				}
				chanRead, ok := unary.(*ast.UnaryExpr)
				if !ok || chanRead.Op != token.ARROW {
					return true
				}
				selector, ok := chanRead.X.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if !m.selectorIsTimer(selector, j.Program.Info) {
					return true
				}
				selectorName := fmt.Sprint(selector.X)
				if selector.Sel.String() != timerChanName {
					return true
				}

				// Verify that the case body contains `timer.Read = true`.
				noRead := walkStmts(comm.Body, func(s ast.Stmt) bool {
					assign, ok := s.(*ast.AssignStmt)
					if !ok || assign.Tok != token.ASSIGN {
						return true
					}
					for i := range assign.Lhs {
						l, r := assign.Lhs[i], assign.Rhs[i]

						// if assignment to correct field in timer.
						assignSelector, ok := l.(*ast.SelectorExpr)
						if !ok {
							return true
						}
						if !m.selectorIsTimer(assignSelector, j.Program.Info) {
							return true
						}
						if fmt.Sprint(assignSelector.X) != selectorName {
							return true
						}
						if assignSelector.Sel.String() != "Read" {
							return true
						}

						// if assigning `true`.
						val, ok := r.(*ast.Ident)
						if !ok {
							return true
						}
						if val.String() == "true" {
							// returning false will short-circuit walkStmts and assign
							// noRead to false instead of the default value of true.
							return false
						}
					}
					return true
				})
				if noRead {
					j.Errorf(comm, "must set timer.Read = true after reading from timer.C (see timeutil/timer.go)")
				}
				return true
			})
		})
	})
}

// Adapted from https://github.com/mdempsky/unconvert/blob/beb68d938016d2dec1d1b078054f4d3db25f97be/unconvert.go#L371-L414.
func checkUnconvert(j *lint.Job) {
	forAllFiles(j, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if len(call.Args) != 1 || call.Ellipsis != token.NoPos {
			return true
		}
		ft, ok := j.Program.Info.Types[call.Fun]
		if !ok {
			j.Errorf(call.Fun, "missing type")
			return true
		}
		if !ft.IsType() {
			// Function call; not a conversion.
			return true
		}
		at, ok := j.Program.Info.Types[call.Args[0]]
		if !ok {
			j.Errorf(call.Args[0], "missing type")
			return true
		}
		if !types.Identical(ft.Type, at.Type) {
			// A real conversion.
			return true
		}
		if isUntypedValue(call.Args[0], j.Program.Info) {
			// Workaround golang.org/issue/13061.
			return true
		}
		// Adapted from https://github.com/mdempsky/unconvert/blob/beb68d938016d2dec1d1b078054f4d3db25f97be/unconvert.go#L416-L430.
		//
		// cmd/cgo generates explicit type conversions that
		// are often redundant when introducing
		// _cgoCheckPointer calls (issue #16).  Users can't do
		// anything about these, so skip over them.
		if ident, ok := call.Fun.(*ast.Ident); ok {
			if ident.Name == "_cgoCheckPointer" {
				return false
			}
		}
		j.Errorf(n, "unnecessary conversion")
		return true
	})
}

// Cribbed from https://github.com/mdempsky/unconvert/blob/beb68d938016d2dec1d1b078054f4d3db25f97be/unconvert.go#L557-L607.
func isUntypedValue(n ast.Expr, info *types.Info) bool {
	switch n := n.(type) {
	case *ast.BinaryExpr:
		switch n.Op {
		case token.SHL, token.SHR:
			// Shifts yield an untyped value if their LHS is untyped.
			return isUntypedValue(n.X, info)
		case token.EQL, token.NEQ, token.LSS, token.GTR, token.LEQ, token.GEQ:
			// Comparisons yield an untyped boolean value.
			return true
		case token.ADD, token.SUB, token.MUL, token.QUO, token.REM,
			token.AND, token.OR, token.XOR, token.AND_NOT,
			token.LAND, token.LOR:
			return isUntypedValue(n.X, info) && isUntypedValue(n.Y, info)
		}
	case *ast.UnaryExpr:
		switch n.Op {
		case token.ADD, token.SUB, token.NOT, token.XOR:
			return isUntypedValue(n.X, info)
		}
	case *ast.BasicLit:
		// Basic literals are always untyped.
		return true
	case *ast.ParenExpr:
		return isUntypedValue(n.X, info)
	case *ast.SelectorExpr:
		return isUntypedValue(n.Sel, info)
	case *ast.Ident:
		if obj, ok := info.Uses[n]; ok {
			if obj.Pkg() == nil && obj.Name() == "nil" {
				// The universal untyped zero value.
				return true
			}
			if b, ok := obj.Type().(*types.Basic); ok && b.Info()&types.IsUntyped != 0 {
				// Reference to an untyped constant.
				return true
			}
		}
	case *ast.CallExpr:
		if b, ok := asBuiltin(n.Fun, info); ok {
			switch b.Name() {
			case "real", "imag":
				return isUntypedValue(n.Args[0], info)
			case "complex":
				return isUntypedValue(n.Args[0], info) && isUntypedValue(n.Args[1], info)
			}
		}
	}

	return false
}

// Cribbed from https://github.com/mdempsky/unconvert/blob/beb68d938016d2dec1d1b078054f4d3db25f97be/unconvert.go#L609-L630.
func asBuiltin(n ast.Expr, info *types.Info) (*types.Builtin, bool) {
	for {
		paren, ok := n.(*ast.ParenExpr)
		if !ok {
			break
		}
		n = paren.X
	}

	ident, ok := n.(*ast.Ident)
	if !ok {
		return nil, false
	}

	obj, ok := info.Uses[ident]
	if !ok {
		return nil, false
	}

	b, ok := obj.(*types.Builtin)
	return b, ok
}

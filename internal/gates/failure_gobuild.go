package gates

import (
	"path"
	"regexp"
	"slices"
	"strings"
)

var goCompileRe = regexp.MustCompile(`^(?:vet: )?((?:[A-Za-z]:)?[^:\s][^:]*\.go):(\d+):(\d+): (.+)$`)

// goBuildPackage names the package a build import path stands for: the external test package
// "p_test [p.test]" is built for p, and "d [p.test]" is d itself, built for the tests of p. Only
// the "[<pkg>.test]" form of its own package drops the "_test" suffix.
func goBuildPackage(importPath string) string {
	first, rest, bracket := strings.Cut(importPath, " [")
	if !bracket {
		return first
	}
	if base, ok := strings.CutSuffix(first, "_test"); ok && rest == base+".test]" {
		return base
	}
	return first
}

// buildFailures gives one record per build error. A package built twice (alone, and for its own
// tests) fails twice with the same error: the record that is already listed is not repeated.
// built is filled with the build import paths reported, so a package whose FailedBuild names one
// gets no second record.
func (r *goReport) buildFailures(scope RepoScope, built, covered map[string]bool) []Failure {
	var out []Failure
	for _, a := range r.builds.list() {
		if !a.failed {
			continue
		}
		pkg := goBuildPackage(a.pkg)
		f := goBuildFailure(pkg, a.lines(), scope)
		if !slices.ContainsFunc(out, func(o Failure) bool {
			return o.Package == f.Package && o.TestLocation == f.TestLocation && o.Message == f.Message
		}) {
			out = append(out, f)
		}
		built[a.pkg] = true
		covered[pkg] = true
	}
	return out
}

// goBuildFailure names the first compiler error as file:line:col with the compiler's message; a
// build failure without one keeps its first line.
func goBuildFailure(pkg string, lines []string, scope RepoScope) Failure {
	f := newFailure(pkg, FailureBuild)
	f.Package, f.Output = pkg, ownOutput(lines)
	for _, line := range lines {
		if m := goCompileRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			f.Kind = FailureCompile
			f.TestLocation = formatLocation(goBuildPath(m[1], pkg, scope), atoi(m[2])) + ":" + m[3]
			f.Message = shorten(m[4], failureMessageMax)
			break
		}
	}
	if f.Kind == FailureBuild {
		f.Message = "build failed"
		for _, line := range lines {
			if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "FAIL") {
				f.Message = shorten(t, failureMessageMax)
				break
			}
		}
	}
	f.Attachments = logAttachment(logExcerpt(lines))
	return f
}

// goBuildPath makes a compiler path repository-relative: "./x.go" is relative to the package.
func goBuildPath(raw, pkg string, scope RepoScope) string {
	p := strings.ReplaceAll(raw, `\`, "/")
	if rest, ok := strings.CutPrefix(p, "./"); ok {
		dir, _ := goPackageDir(pkg, scope.Module)
		return path.Join(dir, rest)
	}
	if rel, ok := scope.relPath(p); ok {
		return rel
	}
	return p
}

// goPackageFailure is a package that failed with no failing test and no build event: a build
// error of an older Go, a panic in TestMain, a timeout or a failed setup.
func goPackageFailure(a *goAcc, scope RepoScope) Failure {
	all := a.lines()
	lines := goOwnLines(all)
	if a.failedBuild != "" {
		return goBuildFailure(a.pkg, lines, scope)
	}
	f := newFailure(a.pkg, FailureSetup)
	f.Package, f.DurationMs, f.Output = a.pkg, durationMs(a.elapsed), ownOutput(all)
	if kind, head := goHeadline(lines); kind != "" {
		fillGoStack(&f, kind, head, lines, scope)
		return f
	}
	f.Message = shorten(firstLine(strings.Join(lines, "\n")), failureMessageMax)
	if f.Message == "" {
		f.Message = "package failed"
	}
	f.Attachments = logAttachment(logExcerpt(lines))
	return f
}

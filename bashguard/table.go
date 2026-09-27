// 缺省只读策略表：保守起步集——不在表=非只读；生产按需经 Table 自定义扩展
// （对标 ZCode 的表驱动形态：每命令声明安全 flag 及其值形态）。
package bashguard

// DefaultTable 内置缺省策略表（副本：调用方改表不影响后续 DefaultTable）。
func DefaultTable() *Table {
	return &Table{Commands: defaultCommands()}
}

func defaultCommands() map[string]Policy {
	ro := func(flags map[string]FlagKind, unsafe ...string) Policy {
		return Policy{ReadOnly: true, SafeFlags: flags, UnsafeFlags: unsafe}
	}
	roNoBare := func(flags map[string]FlagKind, unsafe ...string) Policy {
		return Policy{ReadOnly: true, SafeFlags: flags, UnsafeFlags: unsafe, NoBareOperands: true}
	}
	bools := func(flags ...string) map[string]FlagKind {
		m := map[string]FlagKind{}
		for _, f := range flags {
			m[f] = FlagBool
		}
		return m
	}
	with := func(flags map[string]FlagKind, kv ...string) map[string]FlagKind {
		for i := 0; i+1 < len(kv); i += 2 {
			flags[kv[i]] = FlagKind(kv[i+1])
		}
		return flags
	}

	commands := map[string]Policy{
		// ---- 查看类 ----
		"cat":       ro(bools("-n", "-b", "-E", "-T", "-s", "-A", "-v", "-u", "-e")),
		"ls":        ro(bools("-l", "-a", "-la", "-lah", "-lh", "-1", "-R", "-d", "-F", "-h", "-i", "-r", "-S", "-t", "-Z", "-p", "-A")),
		"head":      ro(with(bools("-q", "-v"), "-n", "string", "-c", "string")),
		"tail":      ro(with(bools("-q", "-v", "-f"), "-n", "string", "-c", "string")),
		"wc":        ro(bools("-l", "-w", "-c", "-m", "-L")),
		"grep":      ro(with(bools("-i", "-v", "-n", "-r", "-R", "-E", "-F", "-w", "-x", "-c", "-l", "-L", "-h", "-H", "-q", "-s", "-o", "-a", "-b", "-P", "-Z", "-m", "-A", "-B", "-C", "-G"), "-e", "string", "-f", "string", "--color", "optional", "--include", "string", "--exclude", "string", "--exclude-dir", "string")),
		"egrep":     ro(bools("-i", "-v", "-n", "-r", "-c", "-l", "-q", "-s", "-o", "-a"), "-e", "string"),
		"fgrep":     ro(bools("-i", "-v", "-n", "-r", "-c", "-l", "-q", "-s", "-o", "-a")),
		"diff":      ro(with(bools("-u", "-c", "-q", "-s", "-r", "-y", "-i", "-w", "-B", "-N", "-a", "-p", "-l", "-h"), "-U", "string", "-L", "string", "-x", "string", "--color", "optional")),
		"find":      ro(with(bools("-print", "-print0", "-ls", "-H", "-L", "-P"), "-name", "string", "-iname", "string", "-path", "string", "-ipath", "string", "-regex", "string", "-type", "string", "-maxdepth", "string", "-mindepth", "string", "-size", "string", "-mtime", "string", "-atime", "string", "-ctime", "string", "-newer", "string", "-user", "string", "-group", "string", "-perm", "string"), "-delete", "-exec", "-execdir", "-ok", "-okdir", "-fprint", "-fprint0", "-fls"),
		"file":      ro(bools("-b", "-z", "-L", "-h", "-i", "-k")),
		"stat":      ro(with(bools("-f", "-L", "-c"), "-t", "string", "-s", "string", "-F", "string")),
		"du":        ro(with(bools("-h", "-s", "-a", "-c", "-k", "-m", "-x", "-H", "-L", "-S", "-t"), "-d", "string", "--max-depth", "string", "--threshold", "string")),
		"df":        ro(bools("-h", "-H", "-k", "-l", "-P", "-T", "-i", "-a", "-B", "-g", "-m")),
		"ps":        ro(with(bools("-a", "-u", "-x", "-e", "-f", "-F", "-l", "-j", "-A", "-r", "-v", "-p"), "-o", "string", "-U", "string", "-G", "string", "-s", "string")),
		"which":     ro(bools("-a", "-s", "-v")),
		"whereis":   ro(with(bools("-b", "-m", "-s", "-u"), "-B", "string", "-M", "string", "-S", "string")),
		"type":      ro(bools("-a", "-t", "-P", "-p", "-f")),
		"command":   ro(bools("-v", "-V", "-p")),
		"uname":     ro(with(bools("-a", "-s", "-n", "-r", "-v", "-m", "-p", "-i", "-o"), "-K", "string")),
		"id":        ro(with(bools("-u", "-g", "-G", "-n", "-r", "-Z", "-z"), "-a", "bool")),
		"whoami":    ro(bools()),
		"hostname":  ro(bools("-f", "-s", "-i", "-I", "-a", "-d", "-y", "-n")),
		"pwd":       ro(bools("-L", "-P")),
		"date":      ro(with(bools("-u", "-R", "-r", "-q", "-I", "-U"), "-d", "string", "-s", "string", "+FORMAT", "string", "-v", "string", "-j", "string", "-f", "string")),
		"env":       ro(with(bools("-i", "-0"), "-", "bool")), // 裸 env=打印环境（只读）
		"printenv":  ro(bools("-0")),
		"echo":      ro(bools("-n", "-e")),
		"printf":    ro(bools()),
		"basename":  ro(bools("-a", "-s", "-z")),
		"dirname":   ro(bools("-z")),
		"realpath":  ro(bools("-e", "-m", "-s", "-q", "-P", "-L", "-z")),
		"readlink":  ro(with(bools("-f", "-e", "-m", "-n", "-q", "-s", "-v", "-z"), "-r", "bool")),
		"md5sum":    ro(bools("-b", "-c", "--tag", "-t", "-z", "--quiet", "--strict", "--status", "--warn")),
		"sha256sum": ro(bools("-b", "-c", "--tag", "-t", "-z", "--quiet", "--strict", "--status", "--warn")),
		"sha1sum":   ro(bools("-b", "-c", "--tag", "-t", "-z", "--quiet", "--strict", "--status", "--warn")),
		"base64":    ro(bools("-d", "-D", "-e", "-i", "-n", "-u")),
		"sort":      ro(with(bools("-b", "-d", "-f", "-g", "-i", "-M", "-h", "-n", "-r", "-R", "-c", "-C", "-u", "-V", "-k", "-t", "-m", "-s", "-z", "-S"), "-T", "string", "--parallel", "string"), "-o", "-o="), // -o 输出文件=写（含折叠 -ro 形态）
		"uniq":      ro(with(bools("-c", "-d", "-D", "-u", "-i", "-s", "-z"), "-f", "string", "-w", "string")),
		"cut":       ro(with(bools("-b", "-c", "-f", "-d", "-s", "-z", "--complement"), "-n", "string", "--output-delimiter", "string")),
		"tr":        ro(bools("-c", "-C", "-d", "-s", "-t", "-u")),
		"rev":       ro(bools()),
		"column":    ro(bools("-t", "-s", "-c", "-n", "-e", "-H", "-R", "-r", "-V", "-N", "-E", "-g", "-F", "-L", "-J")),
		"sed":       ro(with(bools("-n", "-E", "-r", "-z", "-s", "--silent", "--quiet", "--expression", "--file"), "-e", "string", "-f", "string", "--expression", "string", "--file", "string"), "-i", "--in-place", "-i.bak", "-w", "--follow-symlinks"),
		"less":      ro(bools("-N", "-R", "-F", "-X", "-S", "-i", "-n", "-E", "-q", "-Q", "-M", "-m", "-g", "-j", "-w", "-z", "-A", "-B", "-C")),
		"jq":        ro(with(bools("-r", "-c", "-n", "-e", "-s", "-S", "-a", "-C", "-M", "--tab", "-j", "-0", "-f"), "--arg", "string", "--argjson", "string", "--slurpfile", "string", "--rawfile", "string", "--raw-input", "bool", "--jsonargs", "bool"), "--raw-output0", "-o", "--output", "-o="),
		// awk/tee/rm/mv/cp/chmod 等写或不可证明命令刻意不在表（未知=非只读）
	}

	// git 子命令表（核心只读集；不在表=非只读）
	gitSubs := map[string]Policy{
		"status":    ro(with(bools("-s", "-b", "--porcelain", "--short", "--long", "-z", "-v", "--ignored", "--no-renames", "--find-renames"), "-u", "bool", "--untracked-files", "optional", "--column", "optional", "--ahead-behind", "bool")),
		"log":       ro(with(bools("--oneline", "-p", "--stat", "--shortstat", "--graph", "--decorate", "--all", "--follow", "--name-only", "--name-status", "-z", "--no-merges", "--merges", "--first-parent", "--reverse", "--color", "-i", "-g", "--raw", "--numstat", "--dirstat", "--compact-summary", "--format", "-1", "-2", "-3", "-4", "-5", "--relative-date", "--date", "--pretty", "-n"), "--since", "string", "--until", "string", "--author", "string", "--grep", "string", "-G", "string", "-S", "string", "--max-count", "string", "--skip", "string", "-L", "string", "--format=", "string")),
		"diff":      ro(with(bools("--stat", "--shortstat", "--cached", "--staged", "-p", "-u", "--name-only", "--name-status", "--raw", "--numstat", "-z", "--no-color", "--color", "--word-diff", "--check", "--summary", "--exit-code", "--quiet", "-M", "-C", "--find-renames", "--find-copies", "--abbrev", "--full-index", "--binary", "--patch-with-stat", "-R", "-a", "--text", "--ignore-all-space", "--ignore-space-change", "-b", "-w", "--ignore-blank-lines", "--ignore-cr-at-eol", "-U", "--unified", "--function-context", "-W"), "-U", "optional", "--unified", "optional", "--anchored", "string", "--diff-filter", "string", "-l", "string", "--inter-hunk-context", "string", "--output-indicator-new", "string")),
		"show":      ro(with(bools("--stat", "--shortstat", "-s", "--format", "--oneline", "--name-only", "--name-status", "--raw", "--numstat", "-p", "-u", "--no-patch", "--abbrev-commit", "--no-notes", "--notes", "--pretty"), "--format", "optional", "--pretty", "optional", "-S", "string", "-G", "string")),
		"blame":     ro(with(bools("-p", "--porcelain", "--line-porcelain", "-c", "-f", "-n", "-e", "-w", "-M", "-C", "--no-abbrev", "--root", "--show-stats", "--date", "-t", "-l"), "-L", "string", "--since", "string", "-S", "string", "--contents", "string")),
		"rev-parse": ro(bools("--short", "--verify", "--quiet", "-q", "--git-dir", "--show-toplevel", "--is-inside-work-tree", "--is-bare-repository", "--abbrev-ref", "--symbolic-full-name", "--show-prefix", "--show-cdup", "--absolute-git-dir", "--all", "--branches", "--tags", "--remotes", "--default", "--sq", "--revs-only", "--no-revs", "--flags", "--no-flags", "--local-env-vars")),
		"ls-files":  ro(with(bools("-c", "--cached", "-d", "--deleted", "-m", "--modified", "-o", "--others", "-i", "--ignored", "-s", "--stage", "-u", "--unmerged", "-k", "--killed", "-z", "-x", "-X", "--full-name", "--abbrev", "--debug", "-t", "-v", "-f", "--eol", "--sparse"), "--exclude", "string", "--exclude-from", "string", "--exclude-per-directory", "string", "--error-unmatch", "bool")),
		"ls-remote": ro(with(bools("--heads", "-h", "--tags", "-t", "--refs", "--quiet", "-q", "--exit-code", "--get-url", "--symref", "--sort"), "--sort", "optional", "-o", "string")),
		"describe":  ro(with(bools("--all", "--tags", "--contains", "--abbrev", "--candidates", "--exact-match", "--debug", "--long", "--match", "--always", "--first-parent", "--exclude", "--dirty", "--broken"), "--match", "string", "--exclude", "string", "--abbrev", "optional", "--candidates", "optional")),
		"shortlog":  ro(bools("-n", "-s", "-e", "-w", "--summary", "--email", "--numbered", "--committer", "--group")),
		"reflog":    ro(with(bools("--all", "--date", "--relative-date", "--oneline", "--no-abbrev"), "--since", "string", "--until", "string", "-n", "string")),
		"remote": Policy{ReadOnly: true,
			SafeFlags: bools("-v", "--verbose"), // git remote -v = 列表（带 URL）
			Subcommands: map[string]Policy{
				"show":    ro(with(bools("-n"), "--prune", "bool", "--all", "bool")),
				"get-url": ro(bools("--push", "--all")),
			}}, // rename/set-url 等写形态不在子表则拒
		"branch": roNoBare(with(bools("-a", "--all", "-v", "-vv", "--verbose", "--list", "-r", "--remotes", "--show-current", "--no-color", "--color", "-q", "--quiet"), "--contains", "string", "--merged", "string", "--no-merged", "string", "--sort", "string", "--points-at", "string")), // 裸=列表；git branch <名> 创建=写
		"tag":    roNoBare(with(bools("-l", "--list"), "-n", "optional", "--sort", "optional", "--contains", "optional", "--points-at", "optional")),                                                                                                                                          // 裸=列表；git tag <名> 打标=写
		"stash": Policy{ReadOnly: true, NoBareOperands: true, Subcommands: map[string]Policy{ // 裸=push（写）；list/show 只读
			"list": ro(bools()),
			"show": ro(with(bools("-p", "--patch", "--stat", "--name-only", "--name-status", "-u"), "--format", "optional", "--pretty", "optional")),
		}},
		"config": roNoBare(with(bools("-l", "--list", "-z", "--null", "--name-only", "--show-origin", "--show-scope", "--show-scope"), "--get", "string", "--get-all", "string", "--get-regexp", "string", "--type", "optional", "--default", "string", "--get-color", "string", "--get-colorbool", "string"), "--global", "--local", "--system", "--file", "-f", "--unset", "--unset-all", "--replace-all", "--add", "--edit", "-e"), // --unset/--add/--edit 是写；--get 消费键名
		"var":    ro(bools("-l", "--list")),
		"worktree": Policy{ReadOnly: true, NoBareOperands: true, Subcommands: map[string]Policy{
			"list": ro(with(bools("--porcelain", "-v", "--verbose", "-z"), "--expire", "string")),
		}},
		"fsck":          ro(bools("--full", "--no-full", "--strict", "--no-strict", "--unreachable", "--dangling", "--lost-found", "--connectivity-only", "--no-reflogs", "-v", "--verbose", "--progress", "--no-progress")),
		"count-objects": ro(bools("-v", "--verbose", "-H", "--human-readable")),
		"cat-file":      ro(with(bools("-t", "-s", "-e", "-p", "--batch", "--batch-check", "--batch-all-sha1s", "--buffer", "-z", "--follow-symlinks", "--textconv", "--filters"), "--path", "string", "--batch-command", "bool")),
		"show-ref":      ro(bools("--head", "-d", "--dereference", "-s", "--hash", "--abbrev", "--tags", "--heads", "-q", "--verify", "--exclude-existing")),
	}
	commands["git"] = Policy{ReadOnly: true,
		// git 全局前置 flag（git -C dir status / git -c k=v log）
		SafeFlags: with(bools("--no-pager", "--no-optional-locks", "--no-replace-objects", "--literal-pathspecs"),
			"-C", "string", "-c", "string", "--git-dir", "string", "--work-tree", "string"),
		Subcommands: gitSubs}

	// 安全包装：剥壳判定内核
	wrapperFlags := func(kv ...string) Policy {
		return Policy{Wrapper: true, SafeFlags: with(map[string]FlagKind{}, kv...)}
	}
	commands["env"] = Policy{Wrapper: true, SafeFlags: with(bools("-i", "-0"), "-u", "string")} // env KEY=V cmd / env -u NAME cmd
	commands["nice"] = wrapperFlags("-n", "string")
	commands["nohup"] = Policy{Wrapper: true}
	commands["time"] = Policy{Wrapper: true, SafeFlags: bools("-p")}
	commands["stdbuf"] = wrapperFlags("-o0", "bool", "-oL", "bool", "-oM", "string", "-e0", "bool", "-eL", "bool", "-eM", "string", "-i0", "bool", "-iL", "bool", "-iM", "string")
	commands["timeout"] = Policy{Wrapper: true, ConsumeBare: 1, SafeFlags: with(bools("-k", "-s", "--preserve-status", "--foreground", "-v"), "-k", "string", "-s", "string")}
	commands["xargs"] = Policy{Wrapper: true, SafeFlags: with(bools("-r", "-0", "-d", "-t", "-p", "-E", "-n", "-P", "-L", "-l", "-I", "-J", "-S", "-s", "-x", "-a"), "-d", "string", "-E", "string", "-n", "string", "-P", "string", "-L", "string", "-I", "string", "-J", "string", "-S", "string", "-s", "string", "-a", "string")}

	return commands
}

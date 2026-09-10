// Command sshm is a terminal picker for saved SSH servers.
//
// Run it with no arguments to open the picker. Everything else is a
// subcommand; see `sshm help`.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sohail/sshm/internal/frecency"
	"github.com/sohail/sshm/internal/launch"
	"github.com/sohail/sshm/internal/model"
	"github.com/sohail/sshm/internal/probe"
	"github.com/sohail/sshm/internal/search"
	"github.com/sohail/sshm/internal/shellinit"
	"github.com/sohail/sshm/internal/sshconf"
	"github.com/sohail/sshm/internal/store"
	"github.com/sohail/sshm/internal/ui"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sshm: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "connect", "c":
			return cmdConnect(args[1:])
		case "list", "ls":
			return cmdList(args[1:])
		case "import":
			return cmdImport(args[1:])
		case "which":
			return cmdWhich(args[1:])
		case "shell-init":
			return cmdShellInit(args[1:])
		case "path":
			return cmdPath()
		case "help", "-h", "--help":
			usage(os.Stdout)
			return nil
		case "version", "-v", "--version":
			fmt.Println("sshm " + version)
			return nil
		}
		if strings.HasPrefix(args[0], "-") {
			// Unknown flag: fall through to the picker's own flag parsing,
			// which will report it properly.
			return cmdPick(args)
		}
	}
	return cmdPick(args)
}

// ---- the picker -----------------------------------------------------------

func cmdPick(args []string) error {
	// Pull the query out first: Go's flag package stops parsing at the first
	// non-flag argument, so "sshm prod --no-probe" would otherwise swallow the
	// flag into the query.
	words, rest := splitPositional(args)

	fs := flag.NewFlagSet("sshm", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	noProbe := fs.Bool("no-probe", false, "do not check reachability on start")
	timeout := fs.Duration("probe-timeout", probe.DefaultTimeout, "reachability probe timeout")
	fs.Usage = func() { usage(os.Stderr) }
	if err := fs.Parse(rest); err != nil {
		return err
	}
	words = append(words, fs.Args()...)

	hosts, hist, err := load()
	if err != nil {
		return err
	}

	m := ui.New(ui.Options{
		Store:     hosts,
		History:   hist,
		Prober:    probe.New(*timeout, probe.DefaultConcurrency, probe.DefaultTTL),
		Query:     strings.Join(words, " "),
		AutoProbe: !*noProbe,
	})

	// WithAltScreen keeps the user's scrollback intact: when the picker exits,
	// whatever was on screen before is still there.
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return err
	}

	// The terminal is fully restored by the time Run returns, which is why the
	// exec happens here rather than inside the model.
	if m.Launch == nil {
		return nil
	}
	// The picker already recorded the connection, so pass no history here.
	return connectAndRecord(*m.Launch, nil, nil)
}

// ---- subcommands ----------------------------------------------------------

func cmdConnect(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: sshm connect <alias> [-- ssh args...]")
	}
	query, extra := splitExtra(args)
	if query == "" {
		return fmt.Errorf("usage: sshm connect <alias> [-- ssh args...]")
	}

	hosts, hist, err := load()
	if err != nil {
		return err
	}

	h, candidates, ok := search.Resolve(hosts.Hosts(), query)
	if !ok {
		if len(candidates) == 0 {
			return fmt.Errorf("no host matches %q", query)
		}
		names := make([]string, 0, len(candidates))
		for _, c := range candidates {
			names = append(names, c.Alias)
		}
		return fmt.Errorf("%q matches %d hosts: %s", query, len(candidates), strings.Join(names, ", "))
	}

	return connectAndRecord(h, extra, hist)
}

// connectAndRecord resolves the ssh invocation, records the connection, then
// replaces this process with ssh.
//
// The order matters. Preparing first means a missing ssh binary does not leave
// a phantom entry in the history. Recording before the exec is then required,
// because execve never returns: there is no "after" in which to write.
func connectAndRecord(h model.Host, extra []string, hist *frecency.Store) error {
	plan, err := launch.Prepare(h, extra)
	if err != nil {
		return err
	}
	if p := h.CheckIdentity(); p != nil && p.Fatal {
		fmt.Fprintf(os.Stderr, "sshm: warning: %s: %s\n", p.Path, p.Message)
	}
	if hist != nil {
		hist.Record(h.Alias, time.Now())
		if err := hist.Save(); err != nil {
			fmt.Fprintln(os.Stderr, "sshm: could not record history: "+err.Error())
		}
	}
	return plan.Exec()
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "output JSON")
	tag := fs.String("tag", "", "only hosts with this tag")
	if err := fs.Parse(args); err != nil {
		return err
	}

	hosts, hist, err := load()
	if err != nil {
		return err
	}

	list := hosts.Hosts()
	if *tag != "" {
		filtered := list[:0:0]
		for _, h := range list {
			if h.HasTag(*tag) {
				filtered = append(filtered, h)
			}
		}
		list = filtered
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(list)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ALIAS\tTARGET\tTAGS\tLAST USED")
	now := time.Now()
	for _, h := range list {
		when := "never"
		if t, ok := hist.LastUsed(h.Alias); ok {
			when = now.Sub(t).Truncate(time.Minute).String() + " ago"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", h.Alias, h.Target(), strings.Join(h.Tags, ","), when)
	}
	return w.Flush()
}

func cmdWhich(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: sshm which <alias>")
	}
	hosts, _, err := load()
	if err != nil {
		return err
	}
	h, candidates, ok := search.Resolve(hosts.Hosts(), args[0])
	if !ok {
		if len(candidates) == 0 {
			return fmt.Errorf("no host matches %q", args[0])
		}
		for _, c := range candidates {
			fmt.Println(c.Alias + "\t" + c.CommandLine(nil))
		}
		return nil
	}
	fmt.Println(h.CommandLine(nil))
	return nil
}

func cmdImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	from := fs.String("from", sshconf.DefaultPath(), "ssh config file to import")
	dryRun := fs.Bool("dry-run", false, "show what would be imported without saving")
	if err := fs.Parse(args); err != nil {
		return err
	}

	hosts, _, err := load()
	if err != nil {
		return err
	}

	imported, warnings, err := sshconf.ParseFile(*from)
	if err != nil {
		return fmt.Errorf("reading %s: %w", *from, err)
	}
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "sshm: "+w)
	}

	added, skipped := sshconf.Dedup(hosts.Hosts(), imported)
	if len(added) == 0 {
		fmt.Printf("Nothing new to import from %s (%d already known).\n", *from, len(skipped))
		return nil
	}

	for _, h := range added {
		fmt.Printf("  + %-20s %s\n", h.Alias, h.Target())
	}
	if len(skipped) > 0 {
		fmt.Printf("  (%d already exist and were left alone)\n", len(skipped))
	}
	if *dryRun {
		fmt.Println("\nDry run: nothing was saved. Re-run without --dry-run to import.")
		return nil
	}

	n, failed := hosts.AddMany(added)
	if len(failed) > 0 {
		fmt.Fprintf(os.Stderr, "sshm: could not add: %s\n", strings.Join(failed, ", "))
	}
	hosts.SortByAlias()
	if err := hosts.Save(); err != nil {
		return err
	}
	fmt.Printf("\nImported %d hosts into %s\n", n, hosts.Path())
	return nil
}

func cmdShellInit(args []string) error {
	// The shell name is positional and the key is a flag, in either order.
	words, rest := splitPositional(args)

	fs := flag.NewFlagSet("shell-init", flag.ContinueOnError)
	key := fs.String("key", "^S", "key chord to bind, e.g. ^S or ^G")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	words = append(words, fs.Args()...)

	if len(words) == 0 {
		return fmt.Errorf("usage: sshm shell-init <%s> [--key ^S]",
			strings.Join(shellinit.Shells(), "|"))
	}
	snippet, err := shellinit.Snippet(words[0], *key)
	if err != nil {
		return err
	}
	fmt.Print(snippet)
	if hint := shellinit.InstallHint(words[0]); hint != "" {
		fmt.Fprintln(os.Stderr, "\n"+hint)
	}
	return nil
}

// splitPositional peels leading non-flag arguments off args and returns them
// separately from the rest, which still needs flag parsing.
func splitPositional(args []string) (words, rest []string) {
	i := 0
	for i < len(args) && args[i] != "--" && !strings.HasPrefix(args[i], "-") {
		i++
	}
	return args[:i:i], args[i:]
}

func cmdPath() error {
	fmt.Println("hosts:   " + store.ConfigPath())
	fmt.Println("history: " + store.HistoryPath())
	return nil
}

// ---- shared plumbing ------------------------------------------------------

// load opens the host store and history. A parse warning is printed but does
// not stop the tool: being unable to reach your servers because of a typo in a
// config file is a worse failure than showing a slightly incomplete list.
func load() (*store.Store, *frecency.Store, error) {
	hosts, err := store.Load(store.ConfigPath())
	if err != nil {
		if hosts.Len() == 0 {
			return nil, nil, err
		}
		fmt.Fprintln(os.Stderr, "sshm: "+err.Error())
	}

	hist, err := frecency.Load(store.HistoryPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, "sshm: history unreadable, starting fresh: "+err.Error())
	}
	hist.Prune(hosts.Aliases())

	return hosts, hist, nil
}

// splitExtra separates sshm's own arguments from anything after "--", which is
// passed through to ssh untouched.
func splitExtra(args []string) (query string, extra []string) {
	for i, a := range args {
		if a == "--" {
			return strings.Join(args[:i], " "), args[i+1:]
		}
	}
	return strings.Join(args, " "), nil
}

func usage(w *os.File) {
	fmt.Fprint(w, `sshm — find and connect to your servers, fast.

USAGE
  sshm [query]                open the picker, optionally pre-filtered
  sshm connect <alias>        connect directly, no UI
  sshm list [--json] [--tag]  list saved hosts
  sshm which <alias>          print the ssh command for a host
  sshm import [--from PATH]   import hosts from ~/.ssh/config
  sshm shell-init <shell>     print the key binding snippet
  sshm path                   show where config and history live
  sshm version

PICKER FLAGS
  --no-probe                  skip the reachability check on start
  --probe-timeout 900ms       how long to wait for each TCP probe

SEARCH
  type anything               fuzzy match on alias, hostname, tags, user, note
  #tag                        only hosts with that tag
  !#tag                       hide hosts with that tag

QUICK START
  sshm import                 pull in the hosts you already have
  eval "$(sshm shell-init zsh)"    bind ^S to the picker

Anything after -- is passed straight to ssh:
  sshm connect prod-api -- -L 8080:localhost:80
`)
}

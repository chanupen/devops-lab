# CLI reference

[Docs index](../README.md) · [TUI guide](tui.md) ·
[Profiles and stacks](profiles-and-stacks.md)

Generated from the command tree's `--help`. Do not edit this page by hand.
Every lab invocation below displays help only. It does not start a TUI, contact
AWS, inject a drill or change the lab.

> **Danger and cost:** without `--help`, up/down and exercise commands can
> change or destroy resources. Cloud apply/destroy requires approval and can
> incur charges. `assist eval --live` injects a fault; ordinary fixture eval
> does not. See the getting-started and task guides before running operations.
> `labs env` can print temporary credentials; never paste its output into Git.

## Regenerate or check

Run from the checkout with Python 3 and the module's Go toolchain.

```sh
python3 scripts/generate-cli-docs.py
```

**What you will see:** `wrote docs/guides/cli.md`. A temporary static binary is
built under ignored `bin/` and removed afterward. No installed binary changes.

```sh
python3 scripts/generate-cli-docs.py --check
```

**What you will see:** `CLI documentation is current`, or a diff and non-zero
exit status. CI runs this check. Update the generated page with command changes.

## Command help

### lab

```sh
lab --help
```

**What you will see:** the help below. No operation is executed.

```text
With no command, lab opens the TUI. Every TUI action is also one of the commands below.

Usage:
  lab [flags]
  lab [command]

Available Commands:
  assist      Gather read-only evidence and draft an incident note
  cloud       Cloud mode: AWS session and account baseline (lab up --mode cloud runs the lab itself)
  completion  Generate the autocompletion script for the specified shell
  doctor      Check that the host is ready to run the lab
  down        Destroy every deployed stack, dependents first
  drills      Break/fix drills: inject a fault, find it, fix it, against the clock
  extend      Push the cloud lab's expires-at forward (default 1h)
  help        Help about any command
  labs        Hands-on labs: list, start, verify each step, stop
  next        Suggest one local next step without executing or recording it
  plan        Show what lab up (or lab down with --destroy) would change
  start       Check readiness, select a local profile and confirm its plan
  status      Show stacks, the GitOps revision and app URLs
  tunnel      Reach a cloud lab's apps at https://<app>.localtest.me:8443 without a load balancer
  up          Bring the lab up to the profile in lab.yaml
  version     Print lab's version

Flags:
      --config string        path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help                 help for lab
      --langfuse-ca string   PEM CA certificate for the TUI assistant's local Langfuse Gateway
      --state string         path to the state file (default ~/.lab/state.json)
  -v, --version              version for lab

Use "lab [command] --help" for more information about a command.
```

### lab assist

```sh
lab assist --help
```

**What you will see:** the help below. No operation is executed.

```text
Gather read-only evidence and draft an incident note

Usage:
  lab assist [command]

Available Commands:
  alert       Investigate a local alert
  drill       Investigate a local drill
  eval        Measure local incident-note models against recorded drill evidence

Flags:
  -h, --help   help for assist

Use "lab assist [command] --help" for more information about a command.
```

### lab assist alert

```sh
lab assist alert --help
```

**What you will see:** the help below. No operation is executed.

```text
Investigate a local alert

Usage:
  lab assist alert <name> [flags]

Flags:
      --config string         path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help                  help for alert
      --hosted                send selected evidence to a hosted model
      --hosted-model string   hosted model name
      --hosted-url string     hosted OpenAI-compatible HTTPS API base URL
      --label stringArray     select an exact firing-alert label (key=value; repeatable)
      --langfuse-ca string    PEM CA certificate for the local Langfuse Gateway
      --namespace string      select an alert in this namespace
      --rules-first           retain a deterministic cause supported by the evidence
      --state string          path to the state file (default ~/.lab/state.json)
```

### lab assist drill

```sh
lab assist drill --help
```

**What you will see:** the help below. No operation is executed.

```text
Investigate a local drill

Usage:
  lab assist drill <name> [flags]

Flags:
      --config string         path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help                  help for drill
      --hosted                send selected evidence to a hosted model
      --hosted-model string   hosted model name
      --hosted-url string     hosted OpenAI-compatible HTTPS API base URL
      --label stringArray     select an exact firing-alert label (key=value; repeatable)
      --langfuse-ca string    PEM CA certificate for the local Langfuse Gateway
      --namespace string      select an alert in this namespace
      --rules-first           retain a deterministic cause supported by the evidence
      --state string          path to the state file (default ~/.lab/state.json)
```

### lab assist eval

```sh
lab assist eval --help
```

**What you will see:** the help below. No operation is executed.

```text
Measure local incident-note models against recorded drill evidence

Usage:
  lab assist eval [flags]

Flags:
      --config string     path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
      --fixtures string   directory of recorded evaluation fixtures
  -h, --help              help for eval
      --live string       run one named drill through its safe start/abort lifecycle
      --models string     comma-separated local Ollama models (default "llama3.2:3b,qwen2.5:3b")
      --runs int          runs per case and mode (minimum 3) (default 3)
      --state string      path to the state file (default ~/.lab/state.json)
```

### lab cloud

```sh
lab cloud --help
```

**What you will see:** the help below. No operation is executed.

```text
Cloud mode: AWS session and account baseline (lab up --mode cloud runs the lab itself)

Usage:
  lab cloud [command]

Available Commands:
  baseline    AWS account baseline: state bucket, lab-admin role, budget and TTL reaper
  leftovers   List everything still tagged lab=true outside the baseline, stale tagging-index entries apart; changes nothing
  login       Assume lab-admin with MFA and cache the session for up to 4 hours (shared with the aws CLI)
  plan        Show the OpenTofu plans for the cloud registry and cluster stacks; changes nothing

Flags:
  -h, --help   help for cloud

Use "lab cloud [command] --help" for more information about a command.
```

### lab cloud baseline

```sh
lab cloud baseline --help
```

**What you will see:** the help below. No operation is executed.

```text
Runs infra/terraform/aws/baseline with OpenTofu. The first apply uses local state (in
~/.lab/tofu/baseline) and then moves it into the state bucket it created. Run the
first apply with --profile devops: the lab profile assumes lab-admin, which the
baseline creates. Budget alerts go to alert_email in ~/.lab/aws/baseline.tfvars.

Usage:
  lab cloud baseline [command]

Available Commands:
  apply       Show the plan, ask, then apply it
  plan        Show what the baseline would change; changes nothing

Flags:
  -h, --help   help for baseline

Use "lab cloud baseline [command] --help" for more information about a command.
```

### lab cloud baseline apply

```sh
lab cloud baseline apply --help
```

**What you will see:** the help below. No operation is executed.

```text
Show the plan, ask, then apply it

Usage:
  lab cloud baseline apply [flags]

Flags:
      --config string       path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help                help for apply
      --module string       baseline module directory (default: infra/terraform/aws/baseline next to lab.yaml)
      --profile string      AWS CLI profile; use devops for the first apply, before lab-admin exists (default "lab")
      --reaper-zip string   reaper Lambda package (default: bin/reaper.zip next to lab.yaml; make reaper builds it)
      --state string        path to the state file (default ~/.lab/state.json)
```

### lab cloud baseline plan

```sh
lab cloud baseline plan --help
```

**What you will see:** the help below. No operation is executed.

```text
Show what the baseline would change; changes nothing

Usage:
  lab cloud baseline plan [flags]

Flags:
      --config string       path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help                help for plan
      --module string       baseline module directory (default: infra/terraform/aws/baseline next to lab.yaml)
      --profile string      AWS CLI profile; use devops for the first apply, before lab-admin exists (default "lab")
      --reaper-zip string   reaper Lambda package (default: bin/reaper.zip next to lab.yaml; make reaper builds it)
      --state string        path to the state file (default ~/.lab/state.json)
```

### lab cloud leftovers

```sh
lab cloud leftovers --help
```

**What you will see:** the help below. No operation is executed.

```text
List everything still tagged lab=true outside the baseline, stale tagging-index entries apart; changes nothing

Usage:
  lab cloud leftovers [flags]

Flags:
      --config string    path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help             help for leftovers
      --profile string   AWS CLI profile; use devops for the first apply, before lab-admin exists (default "lab")
      --state string     path to the state file (default ~/.lab/state.json)
```

### lab cloud login

```sh
lab cloud login --help
```

**What you will see:** the help below. No operation is executed.

```text
Assume lab-admin with MFA and cache the session for up to 4 hours (shared with the aws CLI)

Usage:
  lab cloud login [flags]

Flags:
      --config string    path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help             help for login
      --profile string   AWS CLI profile; use devops for the first apply, before lab-admin exists (default "lab")
      --state string     path to the state file (default ~/.lab/state.json)
```

### lab cloud plan

```sh
lab cloud plan --help
```

**What you will see:** the help below. No operation is executed.

```text
Runs tofu plan for infra/terraform/aws/cluster/registry (ECR) and infra/terraform/aws/cluster
(VPC, EKS, Karpenter IAM, Pod Identity) against the state bucket and prints both plans.
Nothing is applied: lab up --mode cloud does that, after asking.

Usage:
  lab cloud plan [flags]

Flags:
      --config string   path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help            help for plan
      --state string    path to the state file (default ~/.lab/state.json)
  -v, --verbose         also print tofu init and plan progress on stderr
```

### lab completion

```sh
lab completion --help
```

**What you will see:** the help below. No operation is executed.

```text
Generate the autocompletion script for lab for the specified shell.
See each sub-command's help for details on how to use the generated script.

Usage:
  lab completion [command]

Available Commands:
  bash        Generate the autocompletion script for bash
  fish        Generate the autocompletion script for fish
  powershell  Generate the autocompletion script for powershell
  zsh         Generate the autocompletion script for zsh

Flags:
  -h, --help   help for completion

Use "lab completion [command] --help" for more information about a command.
```

### lab completion bash

```sh
lab completion bash --help
```

**What you will see:** the help below. No operation is executed.

```text
Generate the autocompletion script for the bash shell.

This script depends on the 'bash-completion' package.
If it is not installed already, you can install it via your OS's package manager.

To load completions in your current shell session:

	source <(lab completion bash)

To load completions for every new session, execute once:

#### Linux:

	lab completion bash > /etc/bash_completion.d/lab

#### macOS:

	lab completion bash > $(brew --prefix)/etc/bash_completion.d/lab

You will need to start a new shell for this setup to take effect.

Usage:
  lab completion bash

Flags:
  -h, --help              help for bash
      --no-descriptions   disable completion descriptions
```

### lab completion fish

```sh
lab completion fish --help
```

**What you will see:** the help below. No operation is executed.

```text
Generate the autocompletion script for the fish shell.

To load completions in your current shell session:

	lab completion fish | source

To load completions for every new session, execute once:

	lab completion fish > ~/.config/fish/completions/lab.fish

You will need to start a new shell for this setup to take effect.

Usage:
  lab completion fish [flags]

Flags:
  -h, --help              help for fish
      --no-descriptions   disable completion descriptions
```

### lab completion powershell

```sh
lab completion powershell --help
```

**What you will see:** the help below. No operation is executed.

```text
Generate the autocompletion script for powershell.

To load completions in your current shell session:

	lab completion powershell | Out-String | Invoke-Expression

To load completions for every new session, add the output of the above command
to your powershell profile.

Usage:
  lab completion powershell [flags]

Flags:
  -h, --help              help for powershell
      --no-descriptions   disable completion descriptions
```

### lab completion zsh

```sh
lab completion zsh --help
```

**What you will see:** the help below. No operation is executed.

```text
Generate the autocompletion script for the zsh shell.

If shell completion is not already enabled in your environment you will need
to enable it.  You can execute the following once:

	echo "autoload -U compinit; compinit" >> ~/.zshrc

To load completions in your current shell session:

	source <(lab completion zsh)

To load completions for every new session, execute once:

#### Linux:

	lab completion zsh > "${fpath[1]}/_lab"

#### macOS:

	lab completion zsh > $(brew --prefix)/share/zsh/site-functions/_lab

You will need to start a new shell for this setup to take effect.

Usage:
  lab completion zsh [flags]

Flags:
  -h, --help              help for zsh
      --no-descriptions   disable completion descriptions
```

### lab doctor

```sh
lab doctor --help
```

**What you will see:** the help below. No operation is executed.

```text
Checks Podman, cgroup delegation, kernel limits and modules, SELinux, the GPU stack,
Ollama, tool versions and free memory. With --mode cloud it checks OpenTofu, that the
lab AWS profile assumes lab-admin, and that the state bucket and budget exist. Exits
non-zero if any check fails.
With --fix, print exact remediation commands and approval/sudo labels only; never
execute them. Registry upload permission is untested. Supports --json.

Usage:
  lab doctor [flags]

Flags:
      --config string      path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
      --fix                print fix commands only; never execute them (registry write permission untested)
  -h, --help               help for doctor
      --json               print results as JSON
      --mode string        local: this host; cloud: OpenTofu, the lab profile, the state bucket and the budget (default "local")
      --profile string     memory profile to check against: minimal, standard, full (default "minimal")
      --read-only          observe the local registry without testing upload permission
      --state string       path to the state file (default ~/.lab/state.json)
      --timeout duration   timeout per check (default 10s)
```

### lab down

```sh
lab down --help
```

**What you will see:** the help below. No operation is executed.

```text
Destroys every stack recorded in ~/.lab/state.json in reverse dependency order. With
--stacks, destroys only those stacks and the deployed stacks that need them. In local
mode it first starts the main cluster's nodes and lab-zot when a reboot stopped them, and
the registry's image cache volume is kept unless --purge is given. In cloud mode,
lab down then deletes any network interface, EBS volume or secret still tagged with this
lab's owner and expires-at, and lists anything still tagged lab=true outside the baseline.

Usage:
  lab down [flags]

Examples:
  lab down
  lab down --stacks sample-app

Flags:
      --config string      path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help               help for down
      --mode string        local or cloud (default: mode in lab.yaml); each mode keeps its own state
      --purge              also remove the registry's image cache volume
      --stacks strings     destroy only these stacks and the deployed stacks that need them
      --state string       path to the state file (default ~/.lab/state.json)
      --timeout duration   how long each stack may wait for Argo CD and its apps (image pulls dominate on a slow network) (default 30m0s)
      --yes                do not ask for confirmation
```

### lab drills

```sh
lab drills --help
```

**What you will see:** the help below. No operation is executed.

```text
Without a subcommand, lists readiness and durable progress; --help shows commands.
Drills live in drills/ next to lab.yaml and run on the main lab (local mode). Each injects
one fault into the sample app and times how long you take to find it (lab drills ack) and
to fix it (lab drills verify). lab saves what the fault changes before it goes in, and puts
it back when the drill ends, when you abort, at the time limit, or on the next lab up.

Usage:
  lab drills [flags]
  lab drills [command]

Available Commands:
  abort       Give up: put back everything the drill changed
  ack         Name the cause you found: the right one stops the detect timer
  hint        Show the next hint for the running drill
  history     List finished drill runs and the best times per drill
  list        List the drills with your best times
  start       Inject a drill's fault and start the timers
  status      Show the running drill and its timers
  verify      Check the fix; a pass stops the recover timer and puts the lab back

Flags:
      --config string   path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help            help for drills
      --state string    path to the state file (default ~/.lab/state.json)

Use "lab drills [command] --help" for more information about a command.
```

### lab drills abort

```sh
lab drills abort --help
```

**What you will see:** the help below. No operation is executed.

```text
Puts back the objects lab saved before the fault went in, runs the drill's restore.sh and
checks everything is as it was. Safe to run again after a crash or a failed restore.
--expired (used by the time-limit timer) acts only once the drill's time is up.

Usage:
  lab drills abort [flags]

Flags:
      --config string   path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
      --expired         restore only if the drill's time limit has passed (the timer uses it)
  -h, --help            help for abort
      --run string      with --expired: only this run
      --state string    path to the state file (default ~/.lab/state.json)
```

### lab drills ack

```sh
lab drills ack --help
```

**What you will see:** the help below. No operation is executed.

```text
Names the cause you think you found, from the drill's list (lab drills ack --list). The right
cause stops the detect timer; a wrong one is recorded and the timer runs on. History shows both.

Usage:
  lab drills ack <cause> [flags]

Examples:
  lab drills ack --list
  lab drills ack <cause>

Flags:
      --config string   path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help            help for ack
      --list            list the causes ack takes for the running drill
      --state string    path to the state file (default ~/.lab/state.json)
```

### lab drills hint

```sh
lab drills hint --help
```

**What you will see:** the help below. No operation is executed.

```text
Show the next hint for the running drill

Usage:
  lab drills hint [flags]

Flags:
      --config string   path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help            help for hint
      --state string    path to the state file (default ~/.lab/state.json)
```

### lab drills history

```sh
lab drills history --help
```

**What you will see:** the help below. No operation is executed.

```text
Lists every finished run with its times and acks, then the best times per drill.
--reset deletes history and durable drill summaries (one drill with an ID) after a prompt.

Usage:
  lab drills history [--reset [<id>]] [flags]

Examples:
  lab drills history
  lab drills history --reset dns

Flags:
      --config string   path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help            help for history
      --reset           delete the history (of one drill with an ID), after a prompt
      --state string    path to the state file (default ~/.lab/state.json)
  -y, --yes             with --reset: do not ask
```

### lab drills list

```sh
lab drills list --help
```

**What you will see:** the help below. No operation is executed.

```text
List the drills with your best times

Usage:
  lab drills list [flags]

Flags:
      --config string   path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help            help for list
      --state string    path to the state file (default ~/.lab/state.json)
```

### lab drills start

```sh
lab drills start --help
```

**What you will see:** the help below. No operation is executed.

```text
Saves what the fault will change into ~/.lab/state.json, injects the fault and starts the
timers. --random picks a drill whose stacks are up and does not say which, for interview
practice. One drill at a time, and not while a lab is started.

Usage:
  lab drills start [<id>] [flags]

Examples:
  lab drills start bad-deploy
  lab drills start --random

Flags:
      --config string   path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help            help for start
      --random          pick a drill whose stacks are up, and do not say which
      --state string    path to the state file (default ~/.lab/state.json)
```

### lab drills status

```sh
lab drills status --help
```

**What you will see:** the help below. No operation is executed.

```text
Show the running drill and its timers

Usage:
  lab drills status [flags]

Flags:
      --config string   path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help            help for status
      --state string    path to the state file (default ~/.lab/state.json)
```

### lab drills verify

```sh
lab drills verify --help
```

**What you will see:** the help below. No operation is executed.

```text
Runs the drill's verify script against the main lab (60-second timeout). It only looks.
A pass records the time to recover, then lab puts back everything the drill changed, as
Git has it. Exits 1 when the lab has not recovered yet.

Usage:
  lab drills verify [flags]

Flags:
      --config string   path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help            help for verify
      --state string    path to the state file (default ~/.lab/state.json)
```

### lab extend

```sh
lab extend --help
```

**What you will see:** the help below. No operation is executed.

```text
Moves expires-at forward from the later of now and the current expiry, on every AWS
resource the cloud lab created (through the tagging API), on Karpenter's EC2NodeClass
and in the Load Balancer Controller's defaultTags. The TTL reaper reads that tag.
expires-at never goes more than 8h ahead of now.

Usage:
  lab extend [duration] [flags]

Examples:
  lab extend
  lab extend 2h

Flags:
      --config string   path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help            help for extend
      --state string    path to the state file (default ~/.lab/state.json)
```

### lab help

```sh
lab help --help
```

**What you will see:** the help below. No operation is executed.

```text
Help provides help for any command in the application.
Simply type lab help [path to command] for full details.

Usage:
  lab help [command] [flags]

Flags:
  -h, --help   help for help
```

### lab labs

```sh
lab labs --help
```

**What you will see:** the help below. No operation is executed.

```text
Without a subcommand, lists current and durable progress; --help shows commands.
Labs live in labs/ next to lab.yaml. Scratch stop deletes its cluster and current steps,
not lifetime completions/bests. Progress lives in ~/.lab/state.json (cloud: ~/.lab/cloud/state.json).

Usage:
  lab labs [flags]
  lab labs [command]

Available Commands:
  env         Print the lab scripts' environment as shell exports: eval "$(lab labs env)"
  hint        Show the next hint for a step (default: the current one)
  list        List the labs with your progress
  reset       Return the started lab to its start (asks first)
  solution    Show a step's reference answer (asks first)
  start       Start a lab: make its cluster ready and pick up at your step
  status      Show the started lab: its steps, your progress and the current task
  stop        Stop the started lab; a scratch lab's cluster and progress are deleted
  verify      Check a step (default: the current one); passing it moves you on

Flags:
      --config string   path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help            help for labs
      --mode string     local or cloud (default: mode in lab.yaml); each mode keeps its own state
      --state string    path to the state file (default ~/.lab/state.json)

Use "lab labs [command] --help" for more information about a command.
```

### lab labs env

```sh
lab labs env --help
```

**What you will see:** the help below. No operation is executed.

```text
Prints what the verify and solution scripts run with: KUBECONFIG, the lab registry
(LAB_REGISTRY_PUSH/PULL) and the AWS settings. Locally that is Moto with dummy keys; in
cloud mode it is ECR, the cloud session's temporary keys (they end with the session) and
LAB_AWS_TAGS, the tags every AWS resource a lab creates must carry.

Usage:
  lab labs env [flags]

Flags:
      --config string   path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help            help for env
      --mode string     local or cloud (default: mode in lab.yaml); each mode keeps its own state
      --state string    path to the state file (default ~/.lab/state.json)
```

### lab labs hint

```sh
lab labs hint --help
```

**What you will see:** the help below. No operation is executed.

```text
Show the next hint for a step (default: the current one)

Usage:
  lab labs hint [step] [flags]

Flags:
      --config string   path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help            help for hint
      --mode string     local or cloud (default: mode in lab.yaml); each mode keeps its own state
      --state string    path to the state file (default ~/.lab/state.json)
```

### lab labs list

```sh
lab labs list --help
```

**What you will see:** the help below. No operation is executed.

```text
List the labs with your progress

Usage:
  lab labs list [flags]

Flags:
      --config string   path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help            help for list
      --mode string     local or cloud (default: mode in lab.yaml); each mode keeps its own state
      --state string    path to the state file (default ~/.lab/state.json)
```

### lab labs reset

```sh
lab labs reset --help
```

**What you will see:** the help below. No operation is executed.

```text
Runs the lab's reset.sh against its cluster and starts the steps over. Time spent is kept.

Usage:
  lab labs reset [flags]

Flags:
      --config string   path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help            help for reset
      --mode string     local or cloud (default: mode in lab.yaml); each mode keeps its own state
      --state string    path to the state file (default ~/.lab/state.json)
  -y, --yes             do not ask
```

### lab labs solution

```sh
lab labs solution --help
```

**What you will see:** the help below. No operation is executed.

```text
Show a step's reference answer (asks first)

Usage:
  lab labs solution <step> [flags]

Flags:
      --config string   path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help            help for solution
      --lab string      lab id (default: the started lab)
      --mode string     local or cloud (default: mode in lab.yaml); each mode keeps its own state
      --state string    path to the state file (default ~/.lab/state.json)
  -y, --yes             do not ask
```

### lab labs start

```sh
lab labs start --help
```

**What you will see:** the help below. No operation is executed.

```text
Start a lab: make its cluster ready and pick up at your step

Usage:
  lab labs start <id> [flags]

Examples:
  lab labs start 1
  lab labs start 01-argocd-bootstrap
  lab labs start 1 -v   # stream kind's full output

Flags:
      --config string   path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help            help for start
      --mode string     local or cloud (default: mode in lab.yaml); each mode keeps its own state
      --state string    path to the state file (default ~/.lab/state.json)
  -v, --verbose         stream kind's full output too (it is always in the op log)
```

### lab labs status

```sh
lab labs status --help
```

**What you will see:** the help below. No operation is executed.

```text
Show the started lab: its steps, your progress and the current task

Usage:
  lab labs status [flags]

Flags:
      --config string   path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help            help for status
      --mode string     local or cloud (default: mode in lab.yaml); each mode keeps its own state
      --state string    path to the state file (default ~/.lab/state.json)
```

### lab labs stop

```sh
lab labs stop --help
```

**What you will see:** the help below. No operation is executed.

```text
Deletes a scratch lab's cluster, and with it the lab's steps and time: the next start
begins at step 1 on a new cluster. A lab on the main cluster keeps its steps; its time
starts from zero.

Usage:
  lab labs stop [flags]

Flags:
      --config string   path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help            help for stop
      --mode string     local or cloud (default: mode in lab.yaml); each mode keeps its own state
      --state string    path to the state file (default ~/.lab/state.json)
  -v, --verbose         stream kind's full output too (it is always in the op log)
```

### lab labs verify

```sh
lab labs verify --help
```

**What you will see:** the help below. No operation is executed.

```text
Runs the step's verify script against the lab's cluster, with a 60-second timeout.
It only looks; it never changes anything. Exits 1 when the step is not done yet.

Usage:
  lab labs verify [step] [flags]

Flags:
      --config string   path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help            help for verify
      --mode string     local or cloud (default: mode in lab.yaml); each mode keeps its own state
      --state string    path to the state file (default ~/.lab/state.json)
```

### lab next

```sh
lab next --help
```

**What you will see:** the help below. No operation is executed.

```text
Reads local state, catalogs and fresh observational doctor results. Prints one exact
command and its effect, never executes it or records observations. A successful
suggestion does not mean the lab is healthy. No cloud probes or lab lock.

Usage:
  lab next [flags]

Flags:
      --config string   path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help            help for next
      --json            print one recommendation as JSON
      --state string    path to the state file (default ~/.lab/state.json)
```

### lab plan

```sh
lab plan --help
```

**What you will see:** the help below. No operation is executed.

```text
Compares lab.yaml with ~/.lab/state.json and prints the stacks to apply or destroy,
in dependency order. Changes nothing. Deployed stacks outside the profile are kept
unless --prune is given, matching lab up --prune.

Usage:
  lab plan [flags]

Flags:
      --config string     path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
      --destroy           plan lab down instead of lab up
  -h, --help              help for plan
      --json              print the plan as JSON
      --mode string       local or cloud (default: mode in lab.yaml); each mode keeps its own state
      --prune             plan destroying deployed stacks that are not in the profile
      --revision string   plan lab up --revision (default main)
      --state string      path to the state file (default ~/.lab/state.json)
```

### lab start

```sh
lab start --help
```

**What you will see:** the help below. No operation is executed.

```text
Local Linux only. Selects the largest profile that fits available RAM (4/12/19 GiB),
keeps extraStacks, runs read-only doctor preflight and shows the plan before confirmation.
The profile override is temporary. Active labs/drills must be finished explicitly.
Dry-run never applies, restarts containers, downloads charts or writes state/locks.

Usage:
  lab start [flags]

Flags:
      --config string    path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
      --dry-run          show readiness and the plan without applying or writing
  -h, --help             help for start
      --profile string   temporary profile override: minimal, standard, full (default: available RAM)
      --state string     path to the state file (default ~/.lab/state.json)
      --yes              confirm the local plan without prompting; does not bypass readiness
```

### lab status

```sh
lab status --help
```

**What you will see:** the help below. No operation is executed.

```text
Reads ~/.lab/state.json and lists every stack in dependency order and the Git revision
the gitops stack syncs. Once the cluster is up it adds live node and Argo CD app health,
and once core is up the app URLs. Changes nothing.

Usage:
  lab status [flags]

Flags:
      --config string   path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help            help for status
      --mode string     local or cloud (default: mode in lab.yaml); each mode keeps its own state
      --state string    path to the state file (default ~/.lab/state.json)
```

### lab tunnel

```sh
lab tunnel --help
```

**What you will see:** the help below. No operation is executed.

```text
Port-forwards 127.0.0.1:8080 and :8443 on this host to the lab Gateway's Envoy
Service, so a cloud lab's apps have the same URLs as a local one, with no load
balancer to pay for. From the Mac, ssh -N fedora-lab forwards those
ports as before. Runs until Ctrl-C and reconnects when the Envoy pod changes.
A local lab already publishes 8080 and 8443, so use other ports beside one.

Usage:
  lab tunnel [flags]

Examples:
  lab tunnel
  lab tunnel --http-port 9080 --https-port 9443

Flags:
      --config string    path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help             help for tunnel
      --http-port int    local port for HTTP (default 8080)
      --https-port int   local port for HTTPS (default 8443)
      --mode string      local or cloud (default: mode in lab.yaml); each mode keeps its own state
      --state string     path to the state file (default ~/.lab/state.json)
```

### lab up

```sh
lab up --help
```

**What you will see:** the help below. No operation is executed.

```text
In local mode it first starts the main cluster's nodes and the image cache when they are
stopped, as after a reboot; other stopped lab-* containers are reported, not started.
Then it plans against ~/.lab/state.json and applies stacks in dependency order. Stacks
already applied are kept; failed ones are retried. Deployed stacks outside the profile
are kept unless --prune is given.

Usage:
  lab up [flags]

Examples:
  lab up
  lab up --stacks cluster,registry
  lab up --stacks obs --revision feat/obs
  lab up --stacks gitops --refresh-repo   # after rotating the deploy key
  lab up --mode cloud --ttl 20m

Flags:
      --config string      path to lab.yaml (default ./lab.yaml, then ~/.lab/lab.yaml)
  -h, --help               help for up
      --mode string        local or cloud (default: mode in lab.yaml); each mode keeps its own state
      --prune              destroy deployed stacks that are not in the profile
      --refresh-repo       re-apply the gitops stack so Argo CD reloads its repository Secret from repo.sshKeyFile (a rotated deploy key)
      --revision string    sync the gitops stack from this Git branch to test changes before they merge (default main)
      --stacks strings     apply only these stacks and their dependencies
      --state string       path to the state file (default ~/.lab/state.json)
      --timeout duration   how long each stack may wait for Argo CD and its apps (image pulls dominate on a slow network) (default 30m0s)
      --ttl duration       cloud mode: lifetime of a new lab (default cloud.ttl in lab.yaml, at most 8h); the reaper deletes it after that
      --yes                do not ask before destroying stacks or, in cloud mode, applying to AWS
```

### lab version

```sh
lab version --help
```

**What you will see:** the help below. No operation is executed.

```text
Print lab's version

Usage:
  lab version [flags]

Flags:
  -h, --help   help for version
```

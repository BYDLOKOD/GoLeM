# Fish completion for glm
# Install to: ~/.config/fish/completions/glm.fish

# Disable file completion by default
complete -c glm -f

# Commands
set -l commands session run start status result log list clean kill chain pipeline update doctor config mcp _install _uninstall version help

complete -c glm -n "not __fish_seen_subcommand_from $commands" -a "session" -d "Interactive Claude Code via Z.AI"
complete -c glm -n "not __fish_seen_subcommand_from $commands" -a "run" -d "Sync subagent execution"
complete -c glm -n "not __fish_seen_subcommand_from $commands" -a "start" -d "Async subagent execution"
complete -c glm -n "not __fish_seen_subcommand_from $commands" -a "status" -d "Check job status"
complete -c glm -n "not __fish_seen_subcommand_from $commands" -a "result" -d "Get job stdout"
complete -c glm -n "not __fish_seen_subcommand_from $commands" -a "log" -d "Show file changes"
complete -c glm -n "not __fish_seen_subcommand_from $commands" -a "list" -d "List all jobs"
complete -c glm -n "not __fish_seen_subcommand_from $commands" -a "clean" -d "Remove old jobs"
complete -c glm -n "not __fish_seen_subcommand_from $commands" -a "kill" -d "Terminate job"
complete -c glm -n "not __fish_seen_subcommand_from $commands" -a "chain" -d "Sequential chain execution"
complete -c glm -n "not __fish_seen_subcommand_from $commands" -a "pipeline" -d "DAG pipeline from JSON file"
complete -c glm -n "not __fish_seen_subcommand_from $commands" -a "update" -d "Self-update from GitHub"
complete -c glm -n "not __fish_seen_subcommand_from $commands" -a "doctor" -d "System health check"
complete -c glm -n "not __fish_seen_subcommand_from $commands" -a "config" -d "Manage configuration"
complete -c glm -n "not __fish_seen_subcommand_from $commands" -a "mcp" -d "Start MCP server (JSON-RPC over stdio)"
complete -c glm -n "not __fish_seen_subcommand_from $commands" -a "_install" -d "Run interactive setup"
complete -c glm -n "not __fish_seen_subcommand_from $commands" -a "_uninstall" -d "Remove GoLeM"
complete -c glm -n "not __fish_seen_subcommand_from $commands" -a "version" -d "Show version"
complete -c glm -n "not __fish_seen_subcommand_from $commands" -a "help" -d "Show help"

# Global flags
complete -c glm -l version -s v -d "Show version"
complete -c glm -l help -s h -d "Show help"

# Flags for session, run, start, chain
set -l exec_commands session run start chain

complete -c glm -n "__fish_seen_subcommand_from $exec_commands" -s d -l dir -d "Working directory" -x -a "(__fish_complete_directories)"
complete -c glm -n "__fish_seen_subcommand_from $exec_commands" -s t -l timeout -d "Timeout in seconds" -x
complete -c glm -n "__fish_seen_subcommand_from $exec_commands" -s m -l model -d "Set all model slots" -x -a "glm-5.1 glm-5 glm-4"
complete -c glm -n "__fish_seen_subcommand_from $exec_commands" -l opus -d "Set opus model" -x -a "glm-5.1 glm-5 glm-4"
complete -c glm -n "__fish_seen_subcommand_from $exec_commands" -l sonnet -d "Set sonnet model" -x -a "glm-5.1 glm-5 glm-4"
complete -c glm -n "__fish_seen_subcommand_from $exec_commands" -l haiku -d "Set haiku model" -x -a "glm-5.1 glm-5 glm-4"
complete -c glm -n "__fish_seen_subcommand_from $exec_commands" -l tier -d "Complexity-based routing tier" -x -a "light medium heavy auto"
complete -c glm -n "__fish_seen_subcommand_from $exec_commands" -l unsafe -d "Bypass all permission checks"
complete -c glm -n "__fish_seen_subcommand_from $exec_commands" -l mode -d "Permission mode" -x -a "bypassPermissions acceptEdits default plan"
complete -c glm -n "__fish_seen_subcommand_from $exec_commands" -l system-prompt -d "Append system prompt" -x
complete -c glm -n "__fish_seen_subcommand_from $exec_commands" -l constraint -d "Add a behavior constraint" -x -a "readonly no-create plan-first scope:"
complete -c glm -n "__fish_seen_subcommand_from $exec_commands" -l json -d "JSON output format"

# chain-only flag
complete -c glm -n "__fish_seen_subcommand_from chain" -l continue-on-error -d "Keep running steps after a failure"

# pipeline takes a JSON file as first arg
complete -c glm -n "__fish_seen_subcommand_from pipeline" -F -a "*.json"

# Job ID completion for status, result, log, kill
set -l job_commands status result log kill

function __glm_job_ids
    set -l jobs_dir "$HOME/.claude/subagents"
    if test -d $jobs_dir
        find $jobs_dir -maxdepth 2 -name "job-*" -type d 2>/dev/null | string replace -r '.*/' ''
    end
end

complete -c glm -n "__fish_seen_subcommand_from $job_commands" -a "(__glm_job_ids)"

# list flags
complete -c glm -n "__fish_seen_subcommand_from list" -l status -d "Filter by status" -x -a "queued running done failed timeout killed permission_error"
complete -c glm -n "__fish_seen_subcommand_from list" -l since -d "Filter by time" -x
complete -c glm -n "__fish_seen_subcommand_from list" -l json -d "JSON output"

# clean flags
complete -c glm -n "__fish_seen_subcommand_from clean" -l days -d "Remove jobs older than N days" -x

# config subcommands
complete -c glm -n "__fish_seen_subcommand_from config" -a "show" -d "Show current config"
complete -c glm -n "__fish_seen_subcommand_from config" -a "set" -d "Set config value"

# config keys for set
set -l config_keys model opus_model sonnet_model haiku_model permission_mode debug proxy_enabled proxy_port proxy_idle_timeout effort exclude_dynamic_sections system_prompt
complete -c glm -n "__fish_seen_subcommand_from config; and __fish_seen_subcommand_from set" -a "$config_keys"

# doctor flags
complete -c glm -n "__fish_seen_subcommand_from doctor" -l json -d "JSON output"

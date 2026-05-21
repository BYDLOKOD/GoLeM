# Bash completion for glm
# Source: source /path/to/glm.bash or copy to /usr/share/bash-completion/completions/glm

_glm() {
    local cur prev words cword
    _init_completion || return

    local commands="session run start status result log list clean kill chain pipeline update doctor config mcp _install _uninstall version help"
    local flags="-d -t -m --model --opus --sonnet --haiku --tier --unsafe --mode --system-prompt --constraint --json"
    local config_keys="model opus_model sonnet_model haiku_model permission_mode debug proxy_enabled proxy_port proxy_idle_timeout effort exclude_dynamic_sections system_prompt"
    local status_values="queued running done failed timeout killed permission_error"
    local modes="bypassPermissions acceptEdits default plan"
    local tiers="light medium heavy auto"
    local constraint_keys="readonly no-create plan-first scope:"
    local models="glm-5.1 glm-5 glm-4"

    # Determine command position
    local cmd=""
    local i=1
    while [[ $i -lt $cword ]]; do
        if [[ ${words[$i]} != -* ]]; then
            cmd=${words[$i]}
            break
        fi
        ((i++))
    done

    case $cmd in
        session|run|start|chain)
            # These accept common flags
            case $prev in
                -d)
                    _filedir -d
                    return
                    ;;
                -t)
                    return
                    ;;
                -m|--model|--opus|--sonnet|--haiku)
                    COMPREPLY=($(compgen -W "$models" -- "$cur"))
                    return
                    ;;
                --mode)
                    COMPREPLY=($(compgen -W "$modes" -- "$cur"))
                    return
                    ;;
                --tier)
                    COMPREPLY=($(compgen -W "$tiers" -- "$cur"))
                    return
                    ;;
                --constraint)
                    COMPREPLY=($(compgen -W "$constraint_keys" -- "$cur"))
                    return
                    ;;
                --system-prompt)
                    return
                    ;;
            esac
            if [[ $cur == -* ]]; then
                local local_flags="$flags"
                if [[ "$cmd" == "chain" ]]; then
                    local_flags="$flags --continue-on-error"
                fi
                COMPREPLY=($(compgen -W "$local_flags" -- "$cur"))
                return
            fi
            ;;
        pipeline)
            # First positional is a JSON file
            if [[ $cur != -* ]]; then
                _filedir json
            fi
            return
            ;;
        status|result|log|kill)
            # These take a JOB_ID
            if [[ $cur != -* ]]; then
                # Try to complete job IDs from subagents directory
                local jobs_dir="$HOME/.claude/subagents"
                if [[ -d "$jobs_dir" ]]; then
                    local jobs=$(find "$jobs_dir" -maxdepth 2 -name "job-*" -type d 2>/dev/null | sed 's|.*/||' | head -50)
                    COMPREPLY=($(compgen -W "$jobs" -- "$cur"))
                fi
            fi
            return
            ;;
        list)
            case $prev in
                --status)
                    COMPREPLY=($(compgen -W "$status_values" -- "$cur"))
                    return
                    ;;
                --since)
                    return
                    ;;
            esac
            if [[ $cur == -* ]]; then
                COMPREPLY=($(compgen -W "--status --since --json" -- "$cur"))
            fi
            return
            ;;
        clean)
            case $prev in
                --days)
                    return
                    ;;
            esac
            if [[ $cur == -* ]]; then
                COMPREPLY=($(compgen -W "--days" -- "$cur"))
            fi
            return
            ;;
        config)
            case $prev in
                config)
                    COMPREPLY=($(compgen -W "show set" -- "$cur"))
                    return
                    ;;
                set)
                    COMPREPLY=($(compgen -W "$config_keys" -- "$cur"))
                    return
                    ;;
            esac
            return
            ;;
        doctor)
            if [[ $cur == -* ]]; then
                COMPREPLY=($(compgen -W "--json" -- "$cur"))
            fi
            return
            ;;
        "")
            # No command yet
            if [[ $cur == -* ]]; then
                COMPREPLY=($(compgen -W "--version --help -v -h" -- "$cur"))
            else
                COMPREPLY=($(compgen -W "$commands" -- "$cur"))
            fi
            return
            ;;
    esac
}

complete -F _glm glm

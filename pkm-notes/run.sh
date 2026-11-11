#!/bin/bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "$0")" && pwd)"
DATA_DIR="$SCRIPT_DIR/data"
PORT="${PORT:-8000}"

usage() {
    cat <<'EOF'
Usage: ./run.sh [--data-dir PATH] [--port PORT]
  --data-dir  Directory for SQLite notes (default: ./data)
  --port      HTTP port (default: 8000, or PORT environment variable)
  --help      Show this help

An existing PKM Notes process listening on the selected port is stopped before
the server starts. Other applications are left running; use a different port.
EOF
}

while (($#)); do
    case "$1" in
        --data-dir)
            if (($# < 2)); then
                echo "Error: --data-dir requires a path" >&2
                exit 2
            fi
            DATA_DIR="$2"
            shift 2
            ;;
        --port)
            if (($# < 2)); then
                echo "Error: --port requires a number" >&2
                exit 2
            fi
            PORT="$2"
            shift 2
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        *)
            echo "Unknown option: $1" >&2
            usage >&2
            exit 2
            ;;
    esac
done

if [[ "$DATA_DIR" != /* ]]; then
    DATA_DIR="$PWD/$DATA_DIR"
fi

if [[ ! "$PORT" =~ ^[0-9]{1,5}$ ]] || ((10#$PORT < 1 || 10#$PORT > 65535)); then
    echo "Error: invalid port '$PORT'; expected a number between 1 and 65535." >&2
    exit 2
fi

if ! command -v lsof >/dev/null 2>&1 && ! command -v fuser >/dev/null 2>&1; then
    echo "Error: install lsof or fuser to check whether a port is occupied." >&2
    exit 1
fi

find_listener_pids() {
    if command -v lsof >/dev/null 2>&1; then
        lsof -nP -t -iTCP:"$PORT" -sTCP:LISTEN 2>/dev/null || true
    elif command -v fuser >/dev/null 2>&1; then
        fuser -n tcp "$PORT" 2>/dev/null | tr ' ' '\n' | grep -E '^[0-9]+$' || true
    else
        echo "Error: install lsof or fuser to check whether port $PORT is occupied." >&2
        return 1
    fi
}

is_pkm_notes_process() {
    local pid="$1"
    local owner executable

    [[ -r "/proc/$pid/status" && -e "/proc/$pid/exe" ]] || return 1
    owner="$(ps -o uid= -p "$pid" 2>/dev/null | tr -d ' ')"
    [[ "$owner" == "$EUID" ]] || return 1
    executable="$(basename -- "$(readlink -f "/proc/$pid/exe" 2>/dev/null)")"
    executable="${executable% (deleted)}"
    [[ "$executable" == "pkm-notes-backend" || "$executable" == "simple_server" ]]
}

stop_existing_pkm_notes() {
    local pid attempt
    local -a pids=()
    mapfile -t pids < <(find_listener_pids | sort -u)
    ((${#pids[@]})) || return 0

    for pid in "${pids[@]}"; do
        if [[ ! "$pid" =~ ^[0-9]+$ ]]; then
            echo "Error: could not safely identify the process using port $PORT." >&2
            return 1
        fi
        if ! is_pkm_notes_process "$pid"; then
            echo "Error: port $PORT is occupied by another process (PID $pid); leaving it running." >&2
            echo "Stop that process yourself or start PKM Notes on another port with --port." >&2
            return 1
        fi
    done

    for pid in "${pids[@]}"; do
        echo "Stopping existing PKM Notes process (PID $pid) on port $PORT..."
        kill -TERM "$pid" 2>/dev/null || true
    done

    for attempt in {1..10}; do
        mapfile -t pids < <(find_listener_pids | sort -u)
        ((${#pids[@]})) || return 0
        sleep 0.5
    done

    for pid in "${pids[@]}"; do
        if is_pkm_notes_process "$pid"; then
            echo "PKM Notes process (PID $pid) did not stop; forcing it to exit."
            kill -KILL "$pid" 2>/dev/null || true
        else
            echo "Error: port $PORT is still occupied by another process; leaving it running." >&2
            return 1
        fi
    done
    sleep 0.2
    mapfile -t pids < <(find_listener_pids | sort -u)
    if ((${#pids[@]})); then
        echo "Error: port $PORT is still occupied; cannot safely start PKM Notes." >&2
        return 1
    fi
}

if ! command -v go >/dev/null 2>&1; then
    echo "Error: Go is not installed." >&2
    exit 1
fi

stop_existing_pkm_notes

cd "$SCRIPT_DIR"
echo "Building PKM Notes with SQLite FTS5..."
go build -tags sqlite_fts5 -o "$SCRIPT_DIR/pkm-notes-backend" ./backend

echo "Starting PKM Notes at http://localhost:$PORT"
echo "Data directory: $DATA_DIR"
exec "$SCRIPT_DIR/pkm-notes-backend" --data-dir "$DATA_DIR" --port "$PORT"

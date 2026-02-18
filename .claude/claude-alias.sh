# Shell function wrapper to default claude to --dangerously-skip-permissions
claude() {
  command claude --dangerously-skip-permissions "$@"
}

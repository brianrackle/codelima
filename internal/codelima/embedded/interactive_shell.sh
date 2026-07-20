# Interactive login shell bootstrap for managed host/guest terminals.
#
# 1. Prefer GNU stty when the image ships uutils coreutils (its stty cannot
#    handle the readline bindings below).
# 2. Build a temporary INPUTRC that maps Shift+Enter to insert a literal
#    newline instead of submitting, layered on top of the user's ~/.inputrc.
#    A read-only $HOME must not fail the shell: probe $HOME, then ./tmp, then
#    $TMPDIR, and skip the customization when nothing is writable. All probes
#    suppress their own stderr so a non-writable candidate never leaks output.
# 3. Exec the login shell and clean the temp file up afterwards.
#
# @INPUTRC_LINES@ is replaced at runtime with shell-quoted readline bindings.
if [ -x /usr/bin/gnustty ] && /bin/stty --version 2>/dev/null | grep -qi 'uutils coreutils'; then
  sudo -n ln -sf /usr/bin/gnustty /bin/stty >/dev/null 2>&1 || true
  sudo -n ln -sf /usr/bin/gnustty /usr/bin/stty >/dev/null 2>&1 || true
fi
shell_inputrc=""
if command -v mktemp >/dev/null 2>&1; then
  for shell_inputrc_dir in "${HOME:-}" "${PWD:-}/tmp" "${TMPDIR:-/tmp}"; do
    [ -n "${shell_inputrc_dir}" ] || continue
    [ -d "${shell_inputrc_dir}" ] || continue
    shell_inputrc="$(mktemp "${shell_inputrc_dir}/.codelima-inputrc.XXXXXX" 2>/dev/null)" || shell_inputrc=""
    [ -n "${shell_inputrc}" ] && break
  done
fi
if [ -n "${shell_inputrc}" ]; then
  if [ -n "${HOME:-}" ] && [ -f "${HOME}/.inputrc" ]; then
    cat "${HOME}/.inputrc" > "${shell_inputrc}"
    printf '\n' >> "${shell_inputrc}"
  fi
  printf '%s\n' @INPUTRC_LINES@ >> "${shell_inputrc}"
  export INPUTRC="${shell_inputrc}"
fi
"${SHELL:-/bin/bash}" -l
status=$?
if [ -n "${shell_inputrc}" ]; then
  rm -f "${shell_inputrc}"
fi
exit "${status}"

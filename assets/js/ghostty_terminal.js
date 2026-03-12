import { FitAddon, Terminal, init } from "ghostty-web"

let ghosttyReady

function ensureGhostty() {
  if (!ghosttyReady) {
    ghosttyReady = init()
  }

  return ghosttyReady
}

function websocketUrl(port, token) {
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:"
  return `${protocol}//${window.location.hostname}:${port}/?token=${encodeURIComponent(token)}`
}

function activeStatus(status) {
  return status === "booting" || status === "running"
}

export const GhosttyTerminal = {
  async mounted() {
    this.connectionSignature = null
    this.isTearingDown = false
    this.pendingRenderFrame = null
    this.reconnectTimer = null
    this.fitAddon = null
    this.socket = null
    this.terminal = null
    this.inputDisposable = null
    this.resizeDisposable = null

    await this.connectIfNeeded()
  },

  async updated() {
    await this.connectIfNeeded()
  },

  destroyed() {
    this.teardown()
  },

  async connectIfNeeded() {
    const { port, status, token, terminalId } = this.el.dataset
    const mountTarget = this.el.querySelector("[phx-update='ignore']") || this.el
    const signature = `${terminalId}:${status}:${port}:${token}`

    if (signature === this.connectionSignature) {
      return
    }

    this.connectionSignature = signature
    this.teardown()

    if (!activeStatus(status) || !port || !token) {
      mountTarget.innerHTML = `<div class="terminal-empty">Start the thread to launch the Lima-backed Codex session.</div>`
      return
    }

    await ensureGhostty()

    mountTarget.innerHTML = ""

    this.terminal = new Terminal({
      cursorBlink: true,
      cursorStyle: "block",
      fontFamily: "IBM Plex Mono, monospace",
      fontSize: 14,
      rows: 30,
      cols: 120,
      theme: {
        background: "#101315",
        foreground: "#f3efe5"
      }
    })

    this.fitAddon = new FitAddon()
    this.terminal.loadAddon(this.fitAddon)
    await this.terminal.open(mountTarget)
    this.fitAddon.fit()
    this.fitAddon.observeResize()
    this.inputDisposable = this.terminal.onData((data) => {
      if (this.socket?.readyState === WebSocket.OPEN) {
        this.socket.send(data)
      }
    })

    this.resizeDisposable = this.terminal.onResize(({ cols, rows }) => {
      if (this.socket?.readyState === WebSocket.OPEN) {
        this.socket.send(JSON.stringify({ type: "resize", cols, rows }))
      }
    })

    this.openSocket(Number.parseInt(port, 10), token)
  },

  openSocket(port, token) {
    const url = websocketUrl(port, token)
    this.socket = new WebSocket(url)

    this.socket.addEventListener("open", () => {
      this.terminal?.focus()
      this.writeToTerminal("\u001b[32m[connected]\u001b[0m\r\n")
    })

    this.socket.addEventListener("message", (event) => {
      this.writeToTerminal(event.data)
    })

    this.socket.addEventListener("close", () => {
      if (!this.isTearingDown) {
        this.scheduleReconnect()
      }
    })

    this.socket.addEventListener("error", () => {
      if (!this.isTearingDown) {
        this.scheduleReconnect()
      }
    })
  },

  scheduleReconnect() {
    const { port, status, token } = this.el.dataset

    if (!activeStatus(status) || !port || !token) {
      return
    }

    clearTimeout(this.reconnectTimer)

    this.reconnectTimer = window.setTimeout(() => {
      this.openSocket(Number.parseInt(port, 10), token)
    }, 1200)
  },

  teardown() {
    this.isTearingDown = true
    clearTimeout(this.reconnectTimer)
    this.reconnectTimer = null
    window.cancelAnimationFrame(this.pendingRenderFrame)
    this.pendingRenderFrame = null

    if (this.socket) {
      this.socket.close()
      this.socket = null
    }

    this.inputDisposable?.dispose?.()
    this.inputDisposable = null

    this.resizeDisposable?.dispose?.()
    this.resizeDisposable = null

    if (this.fitAddon) {
      this.fitAddon.dispose()
      this.fitAddon = null
    }

    if (this.terminal) {
      this.terminal.dispose()
      this.terminal = null
    }

    this.isTearingDown = false
  },

  writeToTerminal(data) {
    if (!this.terminal) {
      return
    }

    this.terminal.write(data, () => {
      this.scheduleFullRender()
    })
  },

  scheduleFullRender() {
    if (this.pendingRenderFrame || !this.terminal?.renderer || !this.terminal?.wasmTerm) {
      return
    }

    this.pendingRenderFrame = window.requestAnimationFrame(() => {
      this.pendingRenderFrame = null
      this.forceFullRender()
    })
  },

  forceFullRender() {
    if (!this.terminal?.renderer || !this.terminal?.wasmTerm) {
      return
    }

    this.terminal.renderer.render(
      this.terminal.wasmTerm,
      true,
      this.terminal.viewportY,
      this.terminal,
      this.terminal.scrollbarOpacity ?? 0
    )
  }
}

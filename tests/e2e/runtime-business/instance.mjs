import fs from 'node:fs'
import fsp from 'node:fs/promises'
import net from 'node:net'
import path from 'node:path'
import { spawn } from 'node:child_process'
import { request } from './http.mjs'

export async function assertPortFree(port) {
  await new Promise((resolve, reject) => {
    const server = net.createServer()
    server.once('error', error => reject(new Error(`port ${port} is unavailable: ${error.code ?? error.message}`)))
    server.listen(port, '127.0.0.1', () => server.close(resolve))
  })
}

function wait(ms) { return new Promise(resolve => setTimeout(resolve, ms)) }

export class Instance {
  constructor(options) {
    this.label = options.label
    this.dir = options.dir
    this.port = options.port
    this.prefix = options.prefix ?? ''
    this.binary = options.binary
    this.adminUsername = options.adminUsername
    this.adminPassword = options.adminPassword
    this.runId = options.runId
    this.versionTag = options.versionTag
    this.keepRunning = Boolean(options.keepRunning)
    this.origin = `http://127.0.0.1:${this.port}`
    this.child = null
    this.generation = 0
    this.startTime = null
    this.versionResponse = null
  }

  get target() {
    return { label: this.label, origin: this.origin, prefix: this.prefix, dir: this.dir }
  }

  get dataDir() { return path.join(this.dir, 'data') }
  get databasePath() { return path.join(this.dataDir, 'resource.db') }
  get releaseDir() { return path.join(this.dataDir, 'release-snapshots') }
  get uploadsDir() { return path.join(this.dataDir, 'uploads') }
  get logPath() { return path.join(this.dir, `instance-${this.generation}.log`) }
  get configPath() { return path.join(this.dir, 'config.yaml') }

  async prepare() {
    await assertPortFree(this.port)
    await fsp.mkdir(path.join(this.dataDir, 'uploads'), { recursive: true })
    await fsp.mkdir(this.releaseDir, { recursive: true })
    const lines = [
      'server:',
      `  address: 127.0.0.1:${this.port}`,
    ]
    if (this.prefix) lines.push(`  base_path: ${JSON.stringify(this.prefix)}`)
    lines.push(
      'database:',
      '  driver: sqlite',
      '  sqlite:',
      '    path: data/resource.db',
      'cors:',
      '  allow_origin: "*"',
      '  allow_methods: "GET,POST,OPTIONS"',
      '  allow_headers: "*"',
      '  allow_credentials: false',
      'auth:',
      `  cookie_name: qa_${this.runId.replace(/[^a-zA-Z0-9_]/g, '_')}_${this.label}`,
      '  cookie_secure: false',
      '  allow_insecure_localhost: true',
      `  trusted_origins: [${JSON.stringify(this.origin)}]`,
      '  idle_timeout: 2h',
      '  absolute_timeout: 8h',
      'backup:',
      '  enabled: false',
      '',
    )
    await fsp.writeFile(this.configPath, lines.join('\n'), 'utf8')
  }

  async start() {
    if (this.child) throw new Error(`instance ${this.label} is already running`)
    if (this.generation === 0) await this.prepare()
    this.generation += 1
    this.startTime = new Date().toISOString()
    const logHandle = fs.openSync(this.logPath, 'a')
    this.child = spawn(this.binary, ['--config', this.configPath, '--no-frontend'], {
      cwd: this.dir,
      windowsHide: true,
      detached: this.keepRunning,
      stdio: ['ignore', logHandle, logHandle],
      env: {
        ...process.env,
        RAINBOW_ADMIN_USERNAME: this.adminUsername,
        RAINBOW_ADMIN_INITIAL_PASSWORD: this.adminPassword,
      },
    })
    if (this.keepRunning) this.child.unref()
    this.child.once('exit', () => { try { fs.closeSync(logHandle) } catch {} })
    await this.waitReady()
    this.versionResponse = await this.readVersion()
    return this
  }

  async waitReady(timeoutMs = 45000) {
    const deadline = Date.now() + timeoutMs
    let lastError = ''
    while (Date.now() < deadline) {
      if (this.child && this.child.exitCode !== null) {
        throw new Error(`instance ${this.label} exited during startup (code ${this.child.exitCode}); log=${this.logPath}`)
      }
      try {
        const ping = await request(this.target, '/ping', { timeoutMs: 1500 })
        if (ping.status === 200) return
        lastError = `ping HTTP ${ping.status}`
      } catch (error) {
        lastError = error.message
      }
      await wait(250)
    }
    throw new Error(`instance ${this.label} did not become ready: ${lastError}; log=${this.logPath}`)
  }

  async readVersion() {
    const response = await request(this.target, '/api/v1/version')
    if (response.status !== 200 || response.json?.code !== 200) {
      throw new Error(`version endpoint failed on ${this.label}: ${response.status} ${response.text.slice(0, 200)}`)
    }
    const info = response.json.data?.version_info ?? {}
    if (info.version !== this.versionTag) {
      throw new Error(`instance ${this.label} version mismatch: ${info.version} != ${this.versionTag}`)
    }
    return info
  }

  async stop() {
    if (!this.child) return
    const child = this.child
    let identityVerified = false
    try {
      const info = await this.readVersion()
      identityVerified = info.version === this.versionTag
    } catch {
      identityVerified = child.exitCode === null
    }
    if (!identityVerified) {
      throw new Error(`refusing to stop ${this.label}: instance identity no longer matches run ${this.runId}`)
    }
    const exited = new Promise(resolve => child.once('exit', resolve))
    child.kill()
    const timeout = wait(10000).then(() => 'timeout')
    const result = await Promise.race([exited.then(() => 'exited'), timeout])
    if (result === 'timeout') {
      child.kill('SIGKILL')
      await Promise.race([exited, wait(5000)])
    }
    this.child = null
  }

  async restart() {
    await this.stop()
    await this.start()
  }
}

export function processInfo(instance) {
  return {
    label: instance.label,
    pid: instance.child?.pid ?? null,
    executable: instance.binary,
    started_at: instance.startTime,
    origin: instance.origin,
    prefix: instance.prefix || '/',
    directory: instance.dir,
    config: instance.configPath,
    log: instance.logPath,
    version: instance.versionResponse,
    owned: Boolean(instance.child),
  }
}

export function stopCommand(instance) {
  const pid = instance.child?.pid
  return pid
    ? `Stop-Process -Id ${pid} -Force; # only this process was started by run ${instance.runId}`
    : `no running process for ${instance.label}`
}
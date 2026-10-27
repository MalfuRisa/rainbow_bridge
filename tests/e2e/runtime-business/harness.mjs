import fs from 'node:fs'
import path from 'node:path'
import { setActiveCase, setRequestObserver } from './http.mjs'

export class Harness {
  constructor({ runId, artifactsDir, meta }) {
    this.runId = runId
    this.artifactsDir = artifactsDir
    this.meta = meta
    this.results = []
    this.requests = []
    this.requestCount = 0
    this.defects = []
    this.startedAt = new Date().toISOString()
    fs.mkdirSync(path.join(artifactsDir, 'evidence'), { recursive: true })
    setRequestObserver(entry => {
      this.requestCount += 1
      this.requests.push({ ...entry, at: new Date().toISOString() })
    })
  }

  async case(id, description, fn, options = {}) {
    const before = this.requestCount
    const started = Date.now()
    setActiveCase(id)
    const result = {
      id,
      description,
      status: 'pass',
      requests: 0,
      assertions: [],
      evidence: [],
      details: {},
      startedAt: new Date().toISOString(),
      durationMs: 0,
    }
    try {
      if (options.dependency) {
        const dependency = this.results.find(item => item.id === options.dependency)
        if (dependency && dependency.status !== 'pass') {
          result.status = 'blocked'
          result.details.reason = `dependency ${options.dependency} is ${dependency.status}`
          return result
        }
      }
      const extra = await fn(result)
      if (extra && typeof extra === 'object') {
        result.details = { ...result.details, ...extra }
      }
    } catch (error) {
      result.status = options.kind === 'observation' ? 'observation' : 'fail'
      result.details.error = error.message
      if (options.kind !== 'observation' && !error.recordedDefect) {
        this.defects.push({
          id,
          description,
          message: error.message,
          status: 'candidate_product_defect_or_fixture_error',
        })
      }
    } finally {
      result.requests = this.requestCount - before
      result.durationMs = Date.now() - started
      result.evidence = result.evidence ?? []
      setActiveCase(null)
      this.results.push(result)
      const evidencePath = path.join(this.artifactsDir, 'evidence', `${id}.json`)
      fs.writeFileSync(evidencePath, JSON.stringify(result, null, 2))
    }
    return result
  }

  check(result, condition, message, details) {
    if (!condition) {
      const suffix = details === undefined ? '' : `: ${JSON.stringify(details).slice(0, 800)}`
      throw new Error(`${message}${suffix}`)
    }
    result.assertions.push(message)
  }

  note(result, detail) {
    result.details.notes = [...(result.details.notes ?? []), detail]
  }

  async evidenceFile(name, value) {
    const file = path.join(this.artifactsDir, 'evidence', name)
    fs.writeFileSync(file, typeof value === 'string' ? value : JSON.stringify(value, null, 2))
    return file
  }

  finish({ blockedReason, keptRunning = false, processInfo = [], notes = [] } = {}) {
    const counts = { total: this.results.length, pass: 0, fail: 0, blocked: 0, observation: 0 }
    for (const item of this.results) counts[item.status] = (counts[item.status] ?? 0) + 1
    const summary = {
      run_id: this.runId,
      started_at: this.startedAt,
      finished_at: new Date().toISOString(),
      meta: this.meta,
      counts,
      requests: this.requestCount,
      results: this.results,
      defects: this.defects,
      blocked_reason: blockedReason ?? null,
      kept_running: keptRunning,
      processes: processInfo,
      notes,
    }
    fs.writeFileSync(path.join(this.artifactsDir, 'summary.json'), JSON.stringify(summary, null, 2))
    fs.writeFileSync(path.join(this.artifactsDir, 'requests.jsonl'),
      this.requests.map(row => JSON.stringify(row)).join('\n') + (this.requests.length ? '\n' : ''))
    return summary
  }
}

export function expect(condition, message, details) {
  if (!condition) throw new Error(details === undefined ? message : `${message}: ${JSON.stringify(details).slice(0, 800)}`)
}
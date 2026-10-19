import { expect, test } from '@playwright/test'
import { execFileSync } from 'child_process'
import { targetB, writeEvidence } from './support/env'
import { buildTransferZip } from './support/archives'
import { importPreview, importWithArchiveField } from './support/imports'
import { scopeDevMain, setupSentinelScopes } from './support/scopes'

const CONFIG_COUNT = 2000
const BIG_FILE_BYTES = 15 * 1024 * 1024

function workingSet(pid: string): number | null {
  try {
    const output = execFileSync(
      'powershell.exe',
      ['-NoProfile', '-Command', `[int64](Get-Process -Id ${pid} -ErrorAction Stop).WorkingSet64`],
      { encoding: 'utf8' },
    )
    return Number(output.trim())
  } catch {
    return null
  }
}

test.describe('T21 容量边界 @t21', () => {
  test('2000 条目 + 约 32MiB 展开量:响应、耗时与内存实测 @t21', async () => {
    test.setTimeout(240_000)
    const target = targetB()
    await setupSentinelScopes(target, 't21')

    const configs = Array.from({ length: CONFIG_COUNT }, (_, index) => ({
      alias: `CAP_${String(index + 1).padStart(4, '0')}`,
      name: `容量样本 ${index + 1}`,
      environment_key: scopeDevMain.env,
      pipeline_key: scopeDevMain.pipeline,
      type: 'text',
      content: `capacity-${index + 1}-${'x'.repeat(48)}`,
    }))
    const zeroChunk = Buffer.alloc(BIG_FILE_BYTES)
    const randomChunk = Buffer.from(Array.from({ length: 1024 * 1024 }, (_, i) => (i * 2654435761) % 256))
    const files = [
      { fileId: 'qa-cap-z1', fileName: 'zeros-1.bin', bytes: zeroChunk },
      { fileId: 'qa-cap-z2', fileName: 'zeros-2.bin', bytes: zeroChunk },
      { fileId: 'qa-cap-r1', fileName: 'random-1.bin', bytes: randomChunk },
    ]
    const archive = await buildTransferZip({
      configs,
      files,
      meta: {
        assets: files.map((file) => ({
          file_id: file.fileId, environment_key: scopeDevMain.env, pipeline_key: scopeDevMain.pipeline,
          file_name: file.fileName, content_type: 'application/octet-stream', file_size: file.bytes.length, remark: '',
        })),
      },
    })

    const expandedBytes = configs.reduce((sum, item) => sum + item.content.length, 0) + files.reduce((sum, file) => sum + file.bytes.length, 0)
    const pid = process.env.RB_QA_B_PID ?? ''
    const memoryBefore = pid ? workingSet(pid) : null

    const samples: number[] = []
    const sampler = pid
      ? setInterval(() => {
          const value = workingSet(pid)
          if (value !== null) samples.push(value)
        }, 400)
      : null

    const previewStart = Date.now()
    const preview = await importPreview(target, { bytes: archive, filename: 'capacity.zip' })
    const previewMs = Date.now() - previewStart

    const importStart = Date.now()
    const importResponse = await importWithArchiveField(target, { bytes: archive, filename: 'capacity.zip' }, { overwrite: false })
    const importMs = Date.now() - importStart
    if (sampler) clearInterval(sampler)

    const memoryAfter = pid ? workingSet(pid) : null
    const peak = samples.length ? Math.max(...samples) : memoryAfter
    const memoryDelta = memoryBefore !== null && peak !== null ? peak - memoryBefore : null

    const observed = {
      configCount: CONFIG_COUNT,
      expandedBytes,
      archiveBytes: archive.length,
      archiveBytesWithin4MiB: archive.length <= 4 * 1024 * 1024,
      previewMs,
      importMs,
      importResponse: { code: importResponse.json?.code, error: importResponse.json?.error ?? null, total: importResponse.json?.data?.total ?? null, status: importResponse.status },
      previewCode: preview.json?.code,
      previewSummary: preview.json?.data?.summary ?? null,
      memoryBefore, memoryAfter, memoryPeak: peak, memoryDeltaBytes: memoryDelta,
      thresholds: { requestSeconds: 30, memoryDeltaMiB: 256 },
      budgetAbort: {
        request: previewMs > 30_000 || importMs > 30_000,
        memory: memoryDelta !== null && memoryDelta > 256 * 1024 * 1024,
      },
      note: '阈值是本次停测预算，不是产品 SLO；不做无界压缩炸弹',
    }
    writeEvidence('t21-capacity', observed)

    // 夹具约束必须成立,否则样本不符合 PRD 定义
    expect(observed.archiveBytesWithin4MiB, `压缩包 ${archive.length} 字节超过 4MiB 夹具上限`).toBe(true)
    expect(expandedBytes).toBeLessThanOrEqual(33 * 1024 * 1024)
    // 达到停测阈值时:记录资源预算中止(不写成服务已实现限额)
    if (observed.budgetAbort.request || observed.budgetAbort.memory) {
      test.info().annotations.push({ type: 'observed', description: `资源预算中止: ${JSON.stringify(observed.budgetAbort)}` })
    } else {
      expect(importResponse.json?.code, `大包导入结果: ${importResponse.text.slice(0, 200)}`).toBe(200)
    }
  })
})
import { test } from '@playwright/test'
import { targetB, writeEvidence } from './support/env'
import { buildTransferZip } from './support/archives'
import { scopeDevMain, setupSentinelScopes } from './support/scopes'
import { url } from './support/env'

const TOTAL = 200

test.describe('T15 请求取消 @t15', () => {
  test('发送中取消与处理期间取消:观察服务端是否中断/回滚 @t15', async () => {
    const target = targetB()
    await setupSentinelScopes(target, 't15')

    const configs = Array.from({ length: TOTAL }, (_, index) => ({
      alias: `CANCEL_${String(index + 1).padStart(4, '0')}`,
      name: `取消样本 ${index + 1}`,
      environment_key: scopeDevMain.env,
      pipeline_key: scopeDevMain.pipeline,
      type: 'text',
      content: `cancel-payload-${index + 1}`.repeat(4),
    }))
    const archive = await buildTransferZip({ configs, files: [{ fileId: 'qa-cancel-fid', fileName: 'tiny.png', bytes: Buffer.from('cancel-asset') }] })

    const attempt = async (abortAfterMs: number, label: string) => {
      const controller = new AbortController()
      const timer = setTimeout(() => controller.abort(), abortAfterMs)
      const form = new FormData()
      form.append('environment_key', '')
      form.append('pipeline_key', '')
      form.append('overwrite', 'true')
      form.append('archive', new Blob([new Uint8Array(archive)], { type: 'application/zip' }), `${label}.zip`)
      let outcome: string
      try {
        const response = await fetch(url(target, '/api/v1/transfer/import'), {
          method: 'POST',
          body: form as any,
          signal: controller.signal,
        })
        outcome = `completed(status=${response.status})`
      } catch (error) {
        outcome = `aborted(${(error as Error).name})`
      } finally {
        clearTimeout(timer)
      }
      return outcome
    }

    const sendPhase = await attempt(30, 'cancel-send')
    await new Promise((resolve) => setTimeout(resolve, 4000))
    const afterSendPhase = await countCancelled(target)

    const processingPhase = await attempt(120, 'cancel-processing')
    await new Promise((resolve) => setTimeout(resolve, 6000))
    const afterProcessingPhase = await countCancelled(target)

    const observed = {
      total: TOTAL,
      sendPhase: { outcome: sendPhase, rowsAfter: afterSendPhase },
      processingPhase: { outcome: processingPhase, rowsAfter: afterProcessingPhase },
      verdict: afterSendPhase === 0 && afterProcessingPhase === 0
        ? '未命中:未观察到服务处理或未提交'
        : '命中:客户端取消后服务端仍完成/部分完成了写入',
      note: '客户端断线不能作为回滚依据；以库内实际行数为准',
    }
    writeEvidence('t15-cancel', observed)
  })
})

async function countCancelled(target: ReturnType<typeof targetB>): Promise<number> {
  const { listConfigs } = await import('./support/scopes')
  const rows = await listConfigs(target, scopeDevMain)
  return rows.filter((item) => item.alias.startsWith('CANCEL_')).length
}
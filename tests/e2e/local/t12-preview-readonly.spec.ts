import { expect, test } from '@playwright/test'
import * as fs from 'fs'
import * as path from 'path'
import { dataDir, targetB, writeEvidence } from './support/env'
import { buildTransferZip } from './support/archives'
import { importPreview, importSelective, previewSummary } from './support/imports'
import { listAssets, listConfigs, scopeDevMain, setupSentinelScopes } from './support/scopes'
import { getJson, sha256Hex } from './support/net'

function walkHashes(rootDir: string): Record<string, string> {
  const result: Record<string, string> = {}
  if (!fs.existsSync(rootDir)) return result
  const stack = [rootDir]
  while (stack.length) {
    const current = stack.pop()!
    for (const entry of fs.readdirSync(current, { withFileTypes: true })) {
      const full = path.join(current, entry.name)
      if (entry.isDirectory()) stack.push(full)
      else result[path.relative(rootDir, full).replace(/\\/g, '/')] = sha256Hex(fs.readFileSync(full))
    }
  }
  return result
}

test.describe('T12 预览只读与统计一致性 @t12', () => {
  test('预览两次不产生任何写入;选择导入后的统计与实际落库一致 @t12', async () => {
    const target = targetB()
    await setupSentinelScopes(target, 't12')

    // 一个新配置 + 一个与既有哨兵冲突的配置
    const archive = await buildTransferZip({
      configs: [
        { alias: 'PREVIEW_NEW', name: '预览新项', environment_key: 'qa_dev', pipeline_key: 'main', type: 'text', content: 'new-item' },
        { alias: scopeDevMain.sentinel, name: '预览冲突项', environment_key: 'qa_dev', pipeline_key: 'main', type: 'text', content: 'conflict-item' },
      ],
    })

    const beforeConfigs = (await listConfigs(target, scopeDevMain)).length
    const beforeAssets = (await listAssets(target, scopeDevMain)).length
    const beforeEnvs = (await getJson(target, '/api/v1/environment/list')).json?.data?.list?.length ?? -1
    const beforePipelines = (await getJson(target, '/api/v1/pipeline/list?environment_key=qa_dev')).json?.data?.list?.length ?? -1
    const beforeFiles = walkHashes(dataDir(target))

    const preview1 = await importPreview(target, { bytes: archive, filename: 'preview-1.zip' })
    const preview2 = await importPreview(target, { bytes: archive, filename: 'preview-2.zip' })
    expect(preview1.json?.code, preview1.text.slice(0, 300)).toBe(200)
    expect(preview2.json?.code).toBe(200)

    const afterPreviewConfigs = (await listConfigs(target, scopeDevMain)).length
    const afterPreviewAssets = (await listAssets(target, scopeDevMain)).length
    const afterEnvs = (await getJson(target, '/api/v1/environment/list')).json?.data?.list?.length ?? -1
    const afterPipelines = (await getJson(target, '/api/v1/pipeline/list?environment_key=qa_dev')).json?.data?.list?.length ?? -1
    const afterPreviewFiles = walkHashes(dataDir(target))

    const previewLeftovers = {
      configs: afterPreviewConfigs - beforeConfigs,
      assets: afterPreviewAssets - beforeAssets,
      envs: afterEnvs - beforeEnvs,
      pipelines: afterPipelines - beforePipelines,
      files: JSON.stringify(beforeFiles) !== JSON.stringify(afterPreviewFiles),
    }

    const summary1 = previewSummary(preview1)
    const summary2 = previewSummary(preview2)

    // 执行选择导入:只导入新项(不含冲突哨兵)
    const selective = await importSelective(target, { bytes: archive, filename: 'preview-exec.zip' }, {
      selections: [{ environment_key: 'qa_dev', pipeline_key: 'main', resource_keys: [''] }],
      overwrite: false,
    })
    const afterImportConfigs = await listConfigs(target, scopeDevMain)
    const writtenNew = afterImportConfigs.find((item) => item.alias === 'PREVIEW_NEW')
    const sentinelAfter = afterImportConfigs.find((item) => item.alias === scopeDevMain.sentinel)

    writeEvidence('t12-preview', {
      previewLeftovers,
      summary1, summary2,
      previewSummariesEqual: JSON.stringify(summary1) === JSON.stringify(summary2),
      selectiveResponse: { code: selective.json?.code, total: selective.json?.data?.total ?? null, error: selective.json?.error ?? null },
      writtenNew: Boolean(writtenNew),
      sentinelContentAfter: sentinelAfter?.content,
    })

    // 预览不得创建/修改任何东西
    expect(previewLeftovers.configs, '预览后配置数量变化').toBe(0)
    expect(previewLeftovers.assets, '预览后资源数量变化').toBe(0)
    expect(previewLeftovers.envs).toBe(0)
    expect(previewLeftovers.pipelines).toBe(0)
    expect(previewLeftovers.files, '预览后磁盘文件变化').toBe(false)
    // 两次预览统计一致
    expect(summary2).toEqual(summary1)
    // 导入的摘要只计实际写入:该包只有一个新项
    expect(selective.json?.code, selective.text.slice(0, 300)).toBe(200)
    expect(selective.json?.data?.total, '摘要应只计实际写入').toBe(1)
    expect(writtenNew, '新项未落库').toBeTruthy()
    expect(String(sentinelAfter?.content)).toContain('sentinel:')
  })
})
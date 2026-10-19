import { expect, test } from '@playwright/test'
import { targetA, writeEvidence } from './support/env'
import { postRawJson } from './support/net'
import { getConfigDetail, setupSentinelScopes, scopeDevMain } from './support/scopes'
import { configsByAlias } from './support/db'

/**
 * 大整数必须用原始 JSON 字符串生成,测试端 JavaScript Number 不得先丢精度
 * (PRD §4: 用原始字符串比较数字)。
 */
const BIG_LITERAL = '9007199254740993'

test.describe('T06 数字精度与零值语义 @t06', () => {
  test('大整数不变值、remark/is_perm 边界据实记录 @t06', async () => {
    const target = targetA()
    await setupSentinelScopes(target, 't06')

    // number 类型:以原始 JSON 字符串提交大整数
    const numberAlias = 'QA_BIG_NUMBER'
    let numberKey = String(configsByAlias(target, scopeDevMain.env, scopeDevMain.pipeline, numberAlias)[0]?.resource_key ?? '')
    const numberCreate = numberKey
      ? null
      : await postRawJson(
          target,
          '/api/v1/config/create',
          `{"config":{"environment_key":"qa_dev","pipeline_key":"main","alias":"${numberAlias}","name":"QA 大整数","type":"number","content":"${BIG_LITERAL}"}}`,
        )
    if (!numberKey) {
      expect(numberCreate?.json?.code, `number 大整数创建失败: ${numberCreate?.text.slice(0, 200)}`).toBe(200)
      numberKey = String(numberCreate?.json?.data?.config?.resource_key ?? '')
    }
    const numberDetail = await getConfigDetail(target, scopeDevMain, numberKey)
    const storedNumber: string = numberDetail.json?.data?.config?.content ?? ''

    // object 类型:嵌套同一数值,同样以原始 JSON 字符串提交
    const objectAlias = 'QA_BIG_OBJECT'
    let objectKey = String(configsByAlias(target, scopeDevMain.env, scopeDevMain.pipeline, objectAlias)[0]?.resource_key ?? '')
    const objectCreate = objectKey
      ? null
      : await postRawJson(
          target,
          '/api/v1/config/create',
          `{"config":{"environment_key":"qa_dev","pipeline_key":"main","alias":"${objectAlias}","name":"QA 大整数对象","type":"object","content":"{\\"big\\":${BIG_LITERAL}}"}}`,
        )
    if (!objectKey) {
      expect(objectCreate?.json?.code, `object 大整数创建失败: ${objectCreate?.text.slice(0, 200)}`).toBe(200)
      objectKey = String(objectCreate?.json?.data?.config?.resource_key ?? '')
    }
    const objectDetail = await getConfigDetail(target, scopeDevMain, objectKey)
    const storedObject: string = objectDetail.json?.data?.config?.content ?? ''

    // remark 清空尝试 / is_perm true→false:既有契约缺口观察
    const gapAlias = 'QA_ZERO_GAPS'
    let gapKey = String(configsByAlias(target, scopeDevMain.env, scopeDevMain.pipeline, gapAlias)[0]?.resource_key ?? '')
    if (!gapKey) {
      const { createConfig, updateConfig } = await import('./support/scopes')
      const { config } = await createConfig(target, scopeDevMain, {
        alias: gapAlias, name: 'QA 零值观察', type: 'text', content: 'x', remark: '原始备注',
      })
      gapKey = config!.resource_key
      await updateConfig(target, scopeDevMain, { resource_key: gapKey, name: 'QA 零值观察', type: 'text', content: 'x', remark: '原始备注', is_perm: true })
    }
    const { updateConfig } = await import('./support/scopes')
    await updateConfig(target, scopeDevMain, { resource_key: gapKey, name: 'QA 零值观察', type: 'text', content: 'x', remark: '', is_perm: false })
    const gapDetail = await getConfigDetail(target, scopeDevMain, gapKey)

    const evidence = {
      submittedLiteral: BIG_LITERAL,
      numberStored: storedNumber,
      objectStored: storedObject,
      numberPreserved: storedNumber === BIG_LITERAL,
      objectPreserved: storedObject === `{"big":${BIG_LITERAL}}`,
      remarkAfterClearAttempt: gapDetail.json?.data?.config?.remark,
      isPermAfterFalseAttempt: gapDetail.json?.data?.config?.is_perm,
      notes: {
        remark: 'remark 采用非零更新语义，清空尝试被忽略——既有契约缺口观察',
        is_perm: 'is_perm 只能置 true，无法改回 false——既有契约缺口观察',
      },
    }
    writeEvidence('t06-precision', evidence)

    // 数字不应无提示变值(原始字符串比对,测试端全程不经过 Number)
    expect.soft(storedNumber, `number 大整数被改写: ${storedNumber} ≠ ${BIG_LITERAL}`).toBe(BIG_LITERAL)
    expect.soft(storedObject, `object 大整数被改写: ${storedObject} ≠ {"big":${BIG_LITERAL}}`).toBe(`{"big":${BIG_LITERAL}}`)

    // OBSERVED:非零更新语义缺口
    expect(gapDetail.json?.data?.config?.remark, 'remark 清空尝试的实测结果').toBe('原始备注')
    expect(gapDetail.json?.data?.config?.is_perm, 'is_perm 关闭尝试的实测结果').toBe(true)
  })
})
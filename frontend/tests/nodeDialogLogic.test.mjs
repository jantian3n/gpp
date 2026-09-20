import assert from 'node:assert/strict'
import test from 'node:test'

const logic = await import('../src/components/nodeDialogLogic.ts')

test('输入导入链接时不受已预填线路阻塞', () => {
  assert.deepEqual(
      logic.decideNodeDialogAction('  new-node-token  ', 'game-a', 'http-a'),
      {kind: 'import', url: 'new-node-token'},
  )
})

test('没有导入链接时保存完整的线路选择', () => {
  assert.deepEqual(
      logic.decideNodeDialogAction('', 'game-a', 'http-a'),
      {kind: 'select', game: 'game-a', http: 'http-a'},
  )
})

test('Http 选择直连时始终需要二次确认', () => {
  assert.equal(logic.shouldConfirmHTTPDirect('game-a', '直连'), true)
  assert.equal(logic.shouldConfirmHTTPDirect('直连', '直连'), true)
  assert.equal(logic.shouldConfirmHTTPDirect('game-a', 'http-a'), false)
})

test('内置直连不可删除，普通节点可以删除', () => {
  assert.equal(logic.canDeletePeer({name: '直连', protocol: 'direct'}), false)
  assert.equal(logic.canDeletePeer({name: 'hk', protocol: 'hysteria2'}), true)
})

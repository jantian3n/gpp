// FluentComboBox 的选项。放在 .ts 里而不是 <script setup> 里，
// 因为 <script setup> 不允许出现 ES module 的 export。
export interface ComboOption {
  // 提交给后端的值
  value: string
  // 显示名
  label: string
  // 右侧的次要信息（这里的用法是节点延迟）
  meta?: string
  // 次要信息的语义色，沿用旧界面的分档：<60ms 好、<100ms 一般、其余偏差、
  // 测不到延迟（例如只监听 UDP 的 hysteria2）用 unknown
  tone?: 'good' | 'fair' | 'poor' | 'unknown'
}

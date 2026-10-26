import { useState, type ReactNode } from 'react'
import { Form, Input, Modal, Progress, Tag, Typography } from 'antd'
import type { LicStatus } from './api'
import { faDate, faNum } from './format'
import { brand } from './theme'

export function PageHeader({ title, subtitle, extra }: { title: ReactNode; subtitle?: ReactNode; extra?: ReactNode }) {
  return (
    <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', justifyContent: 'space-between', gap: 12, marginBottom: 20 }}>
      <div>
        <Typography.Title level={3} style={{ margin: 0 }}>{title}</Typography.Title>
        {subtitle && <Typography.Text type="secondary">{subtitle}</Typography.Text>}
      </div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>{extra}</div>
    </div>
  )
}

export function Mono({ children }: { children: ReactNode }) {
  return <span dir="ltr" style={{ fontFamily: 'ui-monospace, Consolas, monospace', unicodeBidi: 'isolate' }}>{children}</span>
}

export const statusTag = (s: string) =>
  ({
    online: <Tag color="green">آنلاین</Tag>,
    offline: <Tag color="red">آفلاین</Tag>,
    error: <Tag color="volcano">خطای اتصال</Tag>,
    unknown: <Tag>نامشخص</Tag>,
  })[s] ?? <Tag>{s}</Tag>

export const licenseTag = (s: LicStatus, expires?: string) => {
  const when = expires ? ` · ${faDate(expires)}` : ''
  switch (s) {
    case 'active':
      return <Tag color="green">لایسنس فعال{when}</Tag>
    case 'grace':
      return <Tag color="orange">در مهلت{when}</Tag>
    case 'expired':
      return <Tag color="red">لایسنس منقضی{when}</Tag>
    default:
      return <Tag>بدون لایسنس</Tag>
  }
}

export const severityTag = (s: string) =>
  s === 'critical' ? <Tag color="red">بحرانی</Tag> : s === 'warning' ? <Tag color="orange">هشدار</Tag> : <Tag color="blue">اطلاع</Tag>

export const kindLabel = (k: string) => (k === 'fortigate' ? 'FortiGate' : k === 'fortiweb' ? 'FortiWeb' : k)

// Usage shows a percentage bar colored by load.
export function Usage({ value }: { value: number | null | undefined }) {
  if (value === null || value === undefined || value < 0) return <Typography.Text type="secondary">—</Typography.Text>
  const color = value >= 90 ? '#CF1322' : value >= 75 ? '#D48806' : brand.primary
  return <Progress percent={Math.round(value)} size="small" strokeColor={color} format={(p) => faNum(p) + '٪'} style={{ minWidth: 110, margin: 0 }} />
}

export function uptimeText(sec: number): string {
  if (!sec) return '—'
  const d = Math.floor(sec / 86400)
  const h = Math.floor((sec % 86400) / 3600)
  const m = Math.floor((sec % 3600) / 60)
  if (d > 0) return `${faNum(d)} روز و ${faNum(h)} ساعت`
  if (h > 0) return `${faNum(h)} ساعت و ${faNum(m)} دقیقه`
  return `${faNum(m)} دقیقه`
}

export function bytesText(n: number): string {
  if (!n) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let v = n
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v >= 100 || i === 0 ? 0 : 1)} ${units[i]}`
}

// useReauth asks for the password and current authenticator code
// before a sensitive action; the server checks both again.
export function useReauth() {
  const [state, setState] = useState<{ title: string; body?: ReactNode; okText: string; run: (v: { password: string; code: string }) => Promise<void> } | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [form] = Form.useForm()

  const ask = (title: string, okText: string, run: (v: { password: string; code: string }) => Promise<void>, body?: ReactNode) => {
    form.resetFields()
    setError(null)
    setState({ title, okText, run, body })
  }

  const modal = (
    <Modal
      open={state !== null}
      title={state?.title}
      okText={state?.okText}
      okButtonProps={{ danger: true, loading: busy }}
      cancelText="انصراف"
      onCancel={() => setState(null)}
      onOk={() => form.submit()}
      destroyOnHidden
    >
      {state?.body}
      <Typography.Paragraph type="secondary">برای ادامه، رمز عبور و کد Authenticator خود را وارد کنید.</Typography.Paragraph>
      {error && <Typography.Paragraph type="danger">{error}</Typography.Paragraph>}
      <Form
        form={form}
        layout="vertical"
        requiredMark={false}
        onFinish={async (v) => {
          if (!state) return
          setBusy(true)
          setError(null)
          try {
            await state.run({ password: v.password, code: String(v.code).trim() })
            setState(null)
          } catch (e) {
            setError((e as Error).message)
          } finally {
            setBusy(false)
          }
        }}
      >
        <Form.Item name="password" label="رمز عبور" rules={[{ required: true, message: 'رمز را وارد کنید' }]}>
          <Input.Password dir="ltr" autoComplete="current-password" />
        </Form.Item>
        <Form.Item name="code" label="کد ۶ رقمی" rules={[{ required: true, pattern: /^\s*\d{6}\s*$/, message: 'کد ۶ رقمی را وارد کنید' }]}>
          <Input dir="ltr" inputMode="numeric" autoComplete="one-time-code" style={{ letterSpacing: 6, textAlign: 'center' }} />
        </Form.Item>
      </Form>
    </Modal>
  )
  return { ask, modal }
}

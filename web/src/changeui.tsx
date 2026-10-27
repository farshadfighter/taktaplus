import { useState, type ReactNode } from 'react'
import { Alert, Button, Card, Col, DatePicker, Form, Input, InputNumber, Radio, Row, Select, Space, Switch, Tag, Typography } from 'antd'
import { useNavigate } from 'react-router'
import type { Dayjs } from 'dayjs'
import { api, type Change, type DeviceRow } from './api'
import { useFetch } from './useFetch'
import { licenseTag } from './ui'

export const kindLabel: Record<string, string> = { signatures: 'استقرار سیگنیچر', firmware: 'ارتقای فرم‌ور', script: 'اسکریپت CLI' }

export const changeStatusTag = (s: string) =>
  ({
    draft: <Tag>پیش‌نویس</Tag>,
    pending: <Tag color="gold">در انتظار تأیید</Tag>,
    approved: <Tag color="blue">تأییدشده</Tag>,
    running: <Tag color="processing">در حال اجرا</Tag>,
    succeeded: <Tag color="green">انجام شد</Tag>,
    failed: <Tag color="red">ناموفق</Tag>,
    partial: <Tag color="orange">بخشی ناموفق</Tag>,
    cancelled: <Tag>لغو شد</Tag>,
    rejected: <Tag color="red">رد شد</Tag>,
  })[s] ?? <Tag>{s}</Tag>

export const targetStatusTag = (s: string) =>
  ({
    pending: <Tag>در صف</Tag>,
    running: <Tag color="processing">در حال اجرا</Tag>,
    succeeded: <Tag color="green">موفق</Tag>,
    failed: <Tag color="red">ناموفق</Tag>,
    skipped: <Tag>اجرا نشد</Tag>,
  })[s] ?? <Tag>{s}</Tag>

type Values = {
  title: string
  description?: string
  device_ids: number[]
  run_mode: 'window' | 'at' | 'now'
  run_at?: Dayjs
  urgent_reason?: string
  concurrency: number
  canary: number
  max_failures: number
  backup_first: boolean
}

// ChangeForm collects what every change needs (devices, timing, rollout)
// around kind-specific fields, and creates the change as a draft.
export function ChangeForm({ kind, spec, children, defaultTitle, filterDevice, extraCheck }: {
  kind: string
  spec: () => Record<string, unknown> | null
  children: ReactNode
  defaultTitle?: string
  filterDevice?: (d: DeviceRow) => boolean
  extraCheck?: () => string | null
}) {
  const [form] = Form.useForm<Values>()
  const navigate = useNavigate()
  const devices = useFetch<{ items: DeviceRow[] }>('/devices')
  const mode = Form.useWatch('run_mode', form)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const all = (devices.data?.items ?? []).filter((d) => (filterDevice ? filterDevice(d) : true))
  const usable = all.filter((d) => d.license === 'active' || d.license === 'grace')

  const submit = async (v: Values) => {
    const s = spec()
    const extra = extraCheck?.()
    if (!s || extra) {
      setError(extra ?? 'مشخصات تغییر کامل نیست')
      return
    }
    setBusy(true)
    setError(null)
    try {
      const c = await api.post<Change>('/changes', {
        kind, title: v.title, description: v.description ?? '', spec: s, device_ids: v.device_ids, run_mode: v.run_mode,
        run_at: v.run_mode === 'at' ? v.run_at?.toISOString() : undefined, urgent_reason: v.urgent_reason ?? '',
        concurrency: v.concurrency, canary: v.canary, max_failures: v.max_failures, backup_first: v.backup_first,
      })
      navigate(`/changes/${c.id}`)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Form form={form} layout="vertical" requiredMark={false} onFinish={submit}
      initialValues={{ title: defaultTitle, run_mode: 'window', concurrency: 4, canary: 1, max_failures: 0, backup_first: true, device_ids: [] }}>
      {error && <Alert type="error" showIcon title={error} style={{ marginBottom: 16 }} />}
      <Row gutter={16}>
        <Col xs={24} xl={14}>
          {children}
          <Card title="دستگاه‌ها" style={{ marginBottom: 16 }}>
            <Form.Item name="device_ids" rules={[{ required: true, type: 'array', min: 1, message: 'حداقل یک دستگاه انتخاب کنید' }]}
              extra="دستگاه‌ها به همین ترتیب اجرا می‌شوند؛ دستگاه‌های اول، دسته‌ی آزمایشی هستند.">
              <Select mode="multiple" showSearch optionFilterProp="label" placeholder="انتخاب دستگاه‌ها"
                options={usable.map((d) => ({ value: d.id, label: `${d.name} · ${d.model || d.kind} ${d.firmware ? 'v' + d.firmware : ''}` }))} />
            </Form.Item>
            <Space wrap size={4}>
              <Button size="small" onClick={() => form.setFieldValue('device_ids', usable.map((d) => d.id))}>همه ({usable.length})</Button>
              {Array.from(new Set(usable.map((d) => d.group_name).filter(Boolean))).map((g) => (
                <Button key={g} size="small" onClick={() => form.setFieldValue('device_ids', usable.filter((d) => d.group_name === g).map((d) => d.id))}>گروه {g}</Button>
              ))}
            </Space>
            {all.length > usable.length && (
              <Typography.Paragraph type="secondary" style={{ fontSize: 12, marginTop: 8, marginBottom: 0 }}>
                {all.filter((d) => !usable.includes(d)).map((d) => <span key={d.id} style={{ marginInlineEnd: 8 }}>{d.name} {licenseTag(d.license)}</span>)}
              </Typography.Paragraph>
            )}
          </Card>
        </Col>
        <Col xs={24} xl={10}>
          <Card title="درخواست تغییر" style={{ marginBottom: 16 }}>
            <Form.Item name="title" label="عنوان" rules={[{ required: true, message: 'عنوان را وارد کنید' }]}><Input /></Form.Item>
            <Form.Item name="description" label="توضیح (برای تأییدکننده)"><Input.TextArea rows={2} /></Form.Item>
            <Form.Item name="run_mode" label="زمان اجرا">
              <Radio.Group options={[{ value: 'window', label: 'پنجره‌ی نگهداری' }, { value: 'at', label: 'زمان مشخص' }, { value: 'now', label: 'فوری' }]} />
            </Form.Item>
            {mode === 'at' && <Form.Item name="run_at" label="زمان" rules={[{ required: true }]}><DatePicker showTime style={{ width: '100%' }} /></Form.Item>}
            {mode === 'now' && (
              <Form.Item name="urgent_reason" label="دلیل اجرای خارج از پنجره" rules={[{ required: true, message: 'دلیل را بنویسید' }]}><Input /></Form.Item>
            )}
            <Space wrap size={16}>
              <Form.Item name="canary" label="دسته‌ی آزمایشی"><InputNumber min={0} max={50} /></Form.Item>
              <Form.Item name="concurrency" label="هم‌زمانی"><InputNumber min={1} max={50} /></Form.Item>
              <Form.Item name="max_failures" label="توقف پس از خطای بیشتر از"><InputNumber min={0} /></Form.Item>
            </Space>
            <Form.Item name="backup_first" label="بکاپ قبل از اجرا" valuePropName="checked"><Switch /></Form.Item>
            <Typography.Paragraph type="secondary" style={{ fontSize: 12 }}>
              درخواست به‌صورت پیش‌نویس ساخته می‌شود؛ پس از بررسی پیش‌نمایش هر دستگاه آن را برای تأیید ارسال کنید.
            </Typography.Paragraph>
            <Button type="primary" htmlType="submit" loading={busy} block>ساخت درخواست و پیش‌نمایش</Button>
          </Card>
        </Col>
      </Row>
    </Form>
  )
}

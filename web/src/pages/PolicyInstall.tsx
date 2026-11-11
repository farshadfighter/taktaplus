import { useState } from 'react'
import { Alert, Button, Card, Col, Empty, Form, Input, Modal, Radio, Row, Space, Spin, Table, Tag, Typography, App as AntApp } from 'antd'
import type { Dayjs } from 'dayjs'
import { Link, useNavigate, useParams } from 'react-router'
import { api, type PolicyPackage, type PolicyPlan } from '../api'
import { faNum } from '../format'
import { JDatePicker } from '../jalali'
import { opLabel } from '../policyui'
import { Mono, PageHeader } from '../ui'
import { useFetch } from '../useFetch'

const summary = (p: PolicyPlan) => {
  if (p.blocked?.length) return <Tag color="red">مسدود</Tag>
  const n = p.ops?.length ?? 0
  return n ? <Tag color="blue">{faNum(n)} تغییر</Tag> : <Tag color="green">بدون تغییر</Tag>
}

export default function PolicyInstall() {
  const { id } = useParams()
  const nav = useNavigate()
  const { message } = AntApp.useApp()
  const pkg = useFetch<{ package: PolicyPackage }>(`/policy/packages/${id}`)
  const preview = useFetch<{ plans: PolicyPlan[] | null; version: number }>(`/policy/packages/${id}/preview`)
  const [sel, setSel] = useState<number | null>(null)
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [form] = Form.useForm<{ run_mode: string; run_at?: Dayjs; urgent_reason?: string }>()
  const mode = Form.useWatch('run_mode', form)
  const plans = preview.data?.plans ?? []
  const version = preview.data?.version ?? 0
  const name = pkg.data?.package.name ?? ''
  const cur = plans.find((p) => p.device_id === sel) ?? plans[0]
  const installable = plans.filter((p) => !p.blocked?.length && (p.ops?.length ?? 0) > 0)

  return (
    <>
      <Typography.Text type="secondary"><Link to={`/policy/${id}`}>{name}</Link> / Install v{faNum(version)}</Typography.Text>
      <PageHeader title={`پیش‌نمایش نصب · ${name} v${faNum(version)}`} subtitle="مقایسه‌ی وضعیت مطلوب با کانفیگ فعلی هر دستگاه (از آخرین بکاپ)"
        extra={<Space>
          <Button onClick={() => nav(`/policy/${id}`)}>بازگشت به ویرایش</Button>
          <Button onClick={() => void preview.reload()} loading={preview.loading}>بازخوانی</Button>
          <Button type="primary" disabled={!installable.length} onClick={() => setOpen(true)}>ارسال درخواست تغییر ({faNum(installable.length)} دستگاه)</Button>
        </Space>} />
      {preview.error && <Alert type="error" showIcon title={preview.error} style={{ marginBottom: 12 }} />}
      {plans.filter((p) => p.blocked?.length).map((p) => (
        <Alert key={`b${p.device_id}`} type="error" showIcon style={{ marginBottom: 8 }}
          title={<>{p.device_name}: {p.blocked!.join('؛ ')}؛ این دستگاه از Install کنار گذاشته می‌شود. <Link to="/policy/objects">رفع در آبجکت‌ها</Link></>} />
      ))}
      {plans.filter((p) => p.warnings?.length).map((p) => (
        <Alert key={`w${p.device_id}`} type="warning" showIcon style={{ marginBottom: 8 }} title={<>{p.device_name}: {p.warnings!.join('؛ ')}</>} />
      ))}
      <Spin spinning={preview.loading && !preview.data}>
        {preview.data && plans.length === 0 && <Card><Empty description="هیچ دستگاهی در این Package نیست." /></Card>}
        {plans.length > 0 && (
          <Row gutter={16} style={{ marginTop: 8 }}>
            <Col xs={24} lg={6}>
              <Space orientation="vertical" style={{ width: '100%' }} size={8}>
                {plans.map((p) => (
                  <Card key={p.device_id} size="small" hoverable onClick={() => setSel(p.device_id)}
                    style={{ borderColor: cur?.device_id === p.device_id ? '#900A3C' : undefined }}>
                    <div style={{ display: 'flex', justifyContent: 'space-between' }}><span>{p.device_name}</span>{summary(p)}</div>
                  </Card>
                ))}
              </Space>
            </Col>
            <Col xs={24} lg={18}>
              {cur && (
                <>
                  <Card title={`عملیات به ترتیب اجرا · ${cur.device_name}`} style={{ marginBottom: 16 }} styles={{ body: { padding: 0 } }}>
                    <Table size="small" pagination={false} rowKey={(_, i) => String(i)} dataSource={cur.ops ?? []}
                      locale={{ emptyText: cur.blocked?.length ? 'دستگاه مسدود است' : 'دستگاه با این نسخه هم‌گام است' }}
                      columns={[
                        { title: '#', key: 'i', width: 50, render: (_, __, i) => faNum(i + 1) },
                        { title: 'عمل', dataIndex: 'action', render: (a: string) => <Tag color={opLabel[a]?.[1]}>{opLabel[a]?.[0] ?? a}</Tag> },
                        { title: 'نوع', dataIndex: 'type', render: (t: string) => (t === 'policy' ? 'Policy' : t) },
                        { title: 'شیء', dataIndex: 'name', render: (n: string) => <Mono>{n}</Mono> },
                        { title: 'جزئیات', dataIndex: 'detail' },
                      ]} />
                  </Card>
                  {cur.cli && (
                    <Card title="معادل CLI" extra={<Typography.Text type="secondary">اجرا روی SSH؛ در صورت خطا کانفیگ از بکاپ پیش از نصب برگردانده می‌شود</Typography.Text>}>
                      <pre dir="ltr" style={{ margin: 0, background: '#0F1B2D', color: '#D7E0EC', borderRadius: 8, padding: 12, fontSize: 12, lineHeight: 1.6, maxHeight: 480, overflow: 'auto' }}>{cur.cli}</pre>
                    </Card>
                  )}
                </>
              )}
            </Col>
          </Row>
        )}
      </Spin>
      <Modal open={open} title={`درخواست نصب ${name} v${faNum(version)}`} okText="ارسال برای تأیید" confirmLoading={busy} onCancel={() => setOpen(false)}
        onOk={() => form.submit()} destroyOnHidden>
        <Form form={form} layout="vertical" initialValues={{ run_mode: 'window' }} onFinish={async (v) => {
          setBusy(true)
          try {
            const c = await api.post<{ id: number }>(`/policy/packages/${id}/install`, {
              version, run_mode: v.run_mode, run_at: v.run_mode === 'at' ? v.run_at?.toISOString() : undefined, urgent_reason: v.urgent_reason ?? '',
            })
            message.success('درخواست تغییر ثبت و برای تأیید ارسال شد')
            nav(`/changes/${c.id}`)
          } catch (e) {
            message.error((e as Error).message)
          } finally {
            setBusy(false)
          }
        }}>
          <Typography.Paragraph type="secondary">
            {faNum(installable.length)} دستگاه نصب می‌شوند. پیش از اجرا از هر دستگاه بکاپ تازه گرفته و پیش‌نمایش دوباره ساخته می‌شود.
          </Typography.Paragraph>
          <Form.Item name="run_mode" label="زمان اجرا">
            <Radio.Group options={[{ value: 'window', label: 'پنجره‌ی نگهداری' }, { value: 'at', label: 'زمان مشخص' }, { value: 'now', label: 'فوری' }]} />
          </Form.Item>
          {mode === 'at' && <Form.Item name="run_at" label="زمان" rules={[{ required: true }]}><JDatePicker showTime style={{ width: '100%' }} /></Form.Item>}
          {mode === 'now' && <Form.Item name="urgent_reason" label="دلیل اجرای خارج از پنجره" rules={[{ required: true, message: 'دلیل را بنویسید' }]}><Input /></Form.Item>}
        </Form>
      </Modal>
    </>
  )
}

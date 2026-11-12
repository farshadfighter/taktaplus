import { useState } from 'react'
import { Alert, Button, Card, Col, Form, Input, Modal, Popconfirm, Row, Select, Space, Switch, Table, Tag, Typography, App as AntApp } from 'antd'
import { api, type APIToken } from './api'
import { faDateTime, faNum } from './format'
import { Mono } from './ui'
import { useFetch } from './useFetch'

const lifetimes = [
  { value: 30, label: '۳۰ روز' }, { value: 90, label: '۹۰ روز' }, { value: 180, label: '۱۸۰ روز' },
  { value: 365, label: 'یک سال' }, { value: 0, label: 'بدون انقضا' },
]

function stateTag(t: APIToken) {
  if (t.revoked_at) return <Tag>باطل‌شده</Tag>
  if (t.expires_at) {
    const days = (new Date(t.expires_at).getTime() - Date.now()) / 86400000
    if (days < 0) return <Tag color="red">منقضی</Tag>
    if (days < 7) return <Tag color="orange">{faNum(Math.ceil(days))} روز تا انقضا</Tag>
  }
  return <Tag color="green">فعال</Tag>
}

// APITokens lists and creates API tokens. roleNames maps role ids to
// labels; example is a GET path shown in the curl sample.
export function APITokens({ roleNames, example }: { roleNames: Record<string, string>; example: string }) {
  const { message } = AntApp.useApp()
  const data = useFetch<{ items: APIToken[]; roles: string[] | null }>('/tokens')
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [created, setCreated] = useState<{ token: APIToken; value: string } | null>(null)
  const [form] = Form.useForm()
  const roles = data.data?.roles ?? []
  const origin = window.location.origin
  return (
    <Card title="توکن‌های API" style={{ marginTop: 16 }}
      extra={<Space>
        <Button href="/api-docs">مستند API</Button>
        <Button type="primary" onClick={() => { setError(null); form.resetFields(); setOpen(true) }}>+ توکن API</Button>
      </Space>}>
      <Typography.Paragraph type="secondary">
        برای یکپارچه‌سازی با سامانه‌های دیگر (SIEM، داشبورد، اسکریپت). هر توکن نقش خودش را دارد و می‌تواند فقط‌خواندنی و محدود به آدرس‌های مشخص باشد.
        ریستور، حذف دستگاه، ارتقا و مدیریت کاربران و توکن‌ها فقط از پنل ممکن است.
      </Typography.Paragraph>
      <Table<APIToken> rowKey="id" size="middle" loading={data.loading} dataSource={data.data?.items ?? []} pagination={false} scroll={{ x: 1000 }}
        locale={{ emptyText: 'هنوز توکنی ساخته نشده است' }}
        rowClassName={(t) => (t.revoked_at ? 'ant-table-row-disabled' : '')}
        columns={[
          { title: 'نام', render: (_, t) => <Space orientation="vertical" size={0}><span>{t.name}</span><Typography.Text type="secondary" style={{ fontSize: 12 }}><Mono>tpk_{t.token_id}_…</Mono></Typography.Text></Space> },
          { title: 'نقش', render: (_, t) => <Space size={4}>{roleNames[t.role] ?? t.role}{t.read_only && <Tag color="blue">فقط‌خواندنی</Tag>}</Space> },
          { title: 'آدرس مجاز', render: (_, t) => (t.allowed_nets?.length ? t.allowed_nets.map((n) => <Tag key={n}><Mono>{n}</Mono></Tag>) : 'همه') },
          { title: 'انقضا', render: (_, t) => (t.expires_at ? faDateTime(t.expires_at) : 'ندارد') },
          { title: 'آخرین استفاده', render: (_, t) => (t.last_used_at ? <>{faDateTime(t.last_used_at)} <Mono>{t.last_ip}</Mono></> : '—') },
          { title: 'سازنده', render: (_, t) => <>{t.created_by}<div><Typography.Text type="secondary" style={{ fontSize: 12 }}>{faDateTime(t.created_at)}</Typography.Text></div></> },
          { title: 'وضعیت', render: (_, t) => stateTag(t) },
          {
            title: '', render: (_, t) => !t.revoked_at && (
              <Popconfirm title={`توکن «${t.name}» همین حالا باطل شود؟`} okText="ابطال" cancelText="انصراف" onConfirm={async () => {
                try {
                  await api.del(`/tokens/${t.id}`)
                  message.success('توکن باطل شد')
                  void data.reload()
                } catch (e) {
                  message.error((e as Error).message)
                }
              }}><Button size="small" type="link" danger>ابطال</Button></Popconfirm>
            ),
          },
        ]} />
      <Modal open={open} title="توکن API جدید" okText="ساخت توکن" cancelText="انصراف" confirmLoading={busy} onCancel={() => setOpen(false)} onOk={() => form.submit()} destroyOnHidden>
        {error && <Alert type="error" showIcon title={error} style={{ marginBottom: 12 }} />}
        <Form form={form} layout="vertical" requiredMark={false} initialValues={{ role: roles.includes('viewer') ? 'viewer' : roles[roles.length - 1], read_only: true, days: 90, allowed_nets: [] }}
          onFinish={async (v) => {
            setBusy(true)
            setError(null)
            try {
              const r = await api.post<{ token: APIToken; value: string }>('/tokens', { ...v, code: String(v.code).trim() })
              setOpen(false)
              setCreated(r)
              void data.reload()
            } catch (e) {
              setError((e as Error).message)
            } finally {
              setBusy(false)
            }
          }}>
          <Form.Item name="name" label="نام" rules={[{ required: true, message: 'نام را وارد کنید' }]}><Input placeholder="مثلاً SIEM مرکز عملیات امنیت" /></Form.Item>
          <Row gutter={12}>
            <Col span={12}><Form.Item name="role" label="نقش" extra="حداکثر هم‌سطح نقش شما"><Select options={roles.map((r) => ({ value: r, label: roleNames[r] ?? r }))} /></Form.Item></Col>
            <Col span={12}><Form.Item name="days" label="اعتبار"><Select options={lifetimes} /></Form.Item></Col>
          </Row>
          <Form.Item name="read_only" label="فقط‌خواندنی (فقط درخواست‌های GET)" valuePropName="checked"><Switch /></Form.Item>
          <Form.Item name="allowed_nets" label="آدرس‌ها یا شبکه‌های مجاز" extra="خالی یعنی از هر آدرسی؛ مثل 10.20.0.0/16 یا 192.0.2.7">
            <Select mode="tags" style={{ direction: 'ltr' }} tokenSeparators={[',', ' ']} open={false} />
          </Form.Item>
          <Typography.Paragraph type="secondary">برای تأیید، رمز ورود و کد Authenticator خود را وارد کنید.</Typography.Paragraph>
          <Row gutter={12}>
            <Col span={14}><Form.Item name="password" label="رمز عبور شما" rules={[{ required: true, message: 'رمز را وارد کنید' }]}><Input.Password dir="ltr" autoComplete="current-password" /></Form.Item></Col>
            <Col span={10}><Form.Item name="code" label="کد ۶ رقمی" rules={[{ required: true, pattern: /^\s*\d{6}\s*$/, message: 'کد ۶ رقمی' }]}><Input dir="ltr" inputMode="numeric" style={{ letterSpacing: 4, textAlign: 'center' }} /></Form.Item></Col>
          </Row>
        </Form>
      </Modal>
      <Modal open={!!created} title={`توکن «${created?.token.name ?? ''}» ساخته شد`} onCancel={() => setCreated(null)} cancelButtonProps={{ style: { display: 'none' } }}
        okText="کپی کردم" onOk={() => setCreated(null)} width={640} destroyOnHidden>
        <Alert type="warning" showIcon style={{ marginBottom: 12 }} title="این مقدار فقط همین یک بار نمایش داده می‌شود" description="آن را در سامانه‌ی مقصد ذخیره کنید؛ TaktaPlus فقط Hash آن را نگه می‌دارد." />
        <Typography.Paragraph copyable={{ text: created?.value }} style={{ direction: 'ltr', background: '#F1F4F9', padding: 10, borderRadius: 6, fontFamily: 'ui-monospace, Consolas, monospace', wordBreak: 'break-all' }}>
          {created?.value}
        </Typography.Paragraph>
        <Typography.Text type="secondary">نمونه:</Typography.Text>
        <pre dir="ltr" style={{ background: '#0F1B2D', color: '#D7E0EC', padding: 10, borderRadius: 6, fontSize: 12, whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
          {`curl -H "Authorization: Bearer ${created?.value ?? ''}" ${origin}/api/v1${example}`}
        </pre>
      </Modal>
    </Card>
  )
}

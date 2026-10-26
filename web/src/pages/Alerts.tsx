import { useState } from 'react'
import { Alert, Button, Card, Form, Input, InputNumber, Modal, Popconfirm, Radio, Select, Space, Switch, Table, Tabs, Tag, Typography, App as AntApp } from 'antd'
import { Link } from 'react-router'
import { api, type AlertRow, type Channel, type Group, type Rule } from '../api'
import { useCan } from '../auth'
import { faDateTime, faNum } from '../format'
import { PageHeader, severityTag } from '../ui'
import { useFetch } from '../useFetch'

const ruleKinds: Record<string, { label: string; unit?: string }> = {
  offline: { label: 'قطع ارتباط با دستگاه' },
  cpu: { label: 'مصرف CPU', unit: '٪' },
  mem: { label: 'مصرف حافظه', unit: '٪' },
  disk: { label: 'مصرف دیسک', unit: '٪' },
  contract_expiry: { label: 'انقضای قرارداد FortiGuard', unit: 'روز قبل' },
  license_expiry: { label: 'انقضای لایسنس تکتاپلاس', unit: 'روز قبل' },
}

const channelKinds: Record<string, string> = { email: 'ایمیل', webhook: 'Webhook', syslog: 'Syslog' }

function RuleModal({ rule, onClose, groups, channels }: { rule: Partial<Rule> | null; onClose: (saved: boolean) => void; groups: Group[]; channels: Channel[] }) {
  const [form] = Form.useForm()
  const [error, setError] = useState<string | null>(null)
  const kind = Form.useWatch('kind', form)
  const save = async (v: Rule) => {
    const body = { ...v, group_id: v.group_id ?? null, channel_ids: v.channel_ids ?? [], threshold: v.threshold ?? 0, duration_minutes: v.duration_minutes ?? 0, repeat_minutes: v.repeat_minutes ?? 0 }
    try {
      if (rule?.id) await api.put(`/alert-rules/${rule.id}`, body)
      else await api.post('/alert-rules', body)
      onClose(true)
    } catch (e) {
      setError((e as Error).message)
    }
  }
  return (
    <Modal open={rule !== null} title={rule?.id ? 'ویرایش قانون هشدار' : 'قانون هشدار جدید'} okText="ذخیره" cancelText="انصراف" destroyOnHidden
      onCancel={() => onClose(false)} onOk={() => form.submit()}
      afterOpenChange={(o) => { if (o) { setError(null); form.setFieldsValue({ kind: 'cpu', severity: 'warning', threshold: 90, duration_minutes: 5, repeat_minutes: 0, enabled: true, channel_ids: [], ...rule }) } }}>
      {error && <Alert type="error" title={error} showIcon style={{ marginBottom: 12 }} />}
      <Form form={form} layout="vertical" requiredMark={false} onFinish={save}>
        <Form.Item name="name" label="نام" rules={[{ required: true, message: 'نام قانون' }]}><Input /></Form.Item>
        <Form.Item name="kind" label="شرط"><Select options={Object.entries(ruleKinds).map(([value, k]) => ({ value, label: k.label }))} /></Form.Item>
        <Space size={16} wrap>
          {kind !== 'offline' && (
            <Form.Item name="threshold" label={`آستانه (${ruleKinds[kind]?.unit ?? ''})`}><InputNumber min={0} max={365} /></Form.Item>
          )}
          <Form.Item name="duration_minutes" label="مدت پایداری (دقیقه)"><InputNumber min={0} max={1440} /></Form.Item>
          <Form.Item name="repeat_minutes" label="تکرار اعلان (دقیقه، ۰ = یک‌بار)"><InputNumber min={0} max={10080} /></Form.Item>
        </Space>
        <Form.Item name="severity" label="شدت">
          <Radio.Group optionType="button" options={[{ value: 'info', label: 'اطلاع' }, { value: 'warning', label: 'هشدار' }, { value: 'critical', label: 'بحرانی' }]} />
        </Form.Item>
        <Form.Item name="group_id" label="دامنه"><Select allowClear placeholder="همه‌ی دستگاه‌ها" options={groups.map((g) => ({ value: g.id, label: g.name }))} /></Form.Item>
        <Form.Item name="channel_ids" label="کانال‌های اعلان"><Select mode="multiple" placeholder="بدون اعلان (فقط ثبت در پنل)" options={channels.map((c) => ({ value: c.id, label: `${c.name} (${channelKinds[c.kind]})` }))} /></Form.Item>
        <Form.Item name="enabled" label="فعال" valuePropName="checked"><Switch /></Form.Item>
      </Form>
    </Modal>
  )
}

function ChannelModal({ channel, onClose }: { channel: Partial<Channel> | null; onClose: (saved: boolean) => void }) {
  const [form] = Form.useForm()
  const [error, setError] = useState<string | null>(null)
  const kind = Form.useWatch('kind', form)
  const save = async (v: { name: string; kind: string; enabled: boolean; to?: string } & Record<string, unknown>) => {
    const config = { ...(v.config as object), to: v.to ? v.to.split(/[\s,;]+/).filter(Boolean) : undefined }
    const body = { name: v.name, kind: v.kind, enabled: v.enabled, config }
    try {
      if (channel?.id) await api.put(`/channels/${channel.id}`, body)
      else await api.post('/channels', body)
      onClose(true)
    } catch (e) {
      setError((e as Error).message)
    }
  }
  return (
    <Modal open={channel !== null} title={channel?.id ? 'ویرایش کانال اعلان' : 'کانال اعلان جدید'} okText="ذخیره" cancelText="انصراف" destroyOnHidden
      onCancel={() => onClose(false)} onOk={() => form.submit()}
      afterOpenChange={(o) => {
        if (!o) return
        setError(null)
        form.setFieldsValue({ kind: 'email', enabled: true, ...channel, config: { smtp_port: 587, smtp_security: 'starttls', protocol: 'udp', ...channel?.config }, to: channel?.config?.to?.join(', ') })
      }}>
      {error && <Alert type="error" title={error} showIcon style={{ marginBottom: 12 }} />}
      <Form form={form} layout="vertical" requiredMark={false} onFinish={save}>
        <Form.Item name="name" label="نام" rules={[{ required: true, message: 'نام کانال' }]}><Input /></Form.Item>
        <Form.Item name="kind" label="نوع">
          <Radio.Group optionType="button" disabled={!!channel?.id} options={Object.entries(channelKinds).map(([value, label]) => ({ value, label }))} />
        </Form.Item>
        {kind === 'email' && (
          <>
            <Space.Compact block>
              <Form.Item name={['config', 'smtp_host']} label="سرور SMTP" style={{ flex: 1 }} rules={[{ required: true }]}><Input dir="ltr" /></Form.Item>
              <Form.Item name={['config', 'smtp_port']} label="پورت"><InputNumber min={1} max={65535} /></Form.Item>
            </Space.Compact>
            <Form.Item name={['config', 'smtp_security']} label="امنیت اتصال">
              <Radio.Group options={[{ value: 'starttls', label: 'STARTTLS' }, { value: 'tls', label: 'TLS' }, { value: 'none', label: 'بدون رمزنگاری' }]} />
            </Form.Item>
            <Space.Compact block>
              <Form.Item name={['config', 'smtp_user']} label="نام کاربری" style={{ flex: 1 }}><Input dir="ltr" autoComplete="off" /></Form.Item>
              <Form.Item name={['config', 'smtp_password']} label="رمز" style={{ flex: 1 }}><Input.Password dir="ltr" autoComplete="new-password" /></Form.Item>
            </Space.Compact>
            <Form.Item name={['config', 'from']} label="فرستنده" rules={[{ required: true }]}><Input dir="ltr" placeholder="TaktaPlus <noc@example.com>" /></Form.Item>
            <Form.Item name="to" label="گیرندگان (با کاما جدا کنید)" rules={[{ required: true }]}><Input dir="ltr" /></Form.Item>
          </>
        )}
        {kind === 'webhook' && (
          <>
            <Form.Item name={['config', 'url']} label="آدرس" rules={[{ required: true }]}><Input dir="ltr" placeholder="https://…" /></Form.Item>
            <Form.Item name={['config', 'secret']} label="کلید امضا (اختیاری)" extra="امضای HMAC-SHA256 بدنه در هدر X-TaktaPlus-Signature ارسال می‌شود.">
              <Input.Password dir="ltr" autoComplete="new-password" />
            </Form.Item>
          </>
        )}
        {kind === 'syslog' && (
          <Space.Compact block>
            <Form.Item name={['config', 'address']} label="آدرس (host:port)" style={{ flex: 1 }} rules={[{ required: true }]}><Input dir="ltr" placeholder="10.0.0.5:514" /></Form.Item>
            <Form.Item name={['config', 'protocol']} label="پروتکل"><Select style={{ width: 90 }} options={[{ value: 'udp', label: 'UDP' }, { value: 'tcp', label: 'TCP' }]} /></Form.Item>
          </Space.Compact>
        )}
        <Form.Item name="enabled" label="فعال" valuePropName="checked"><Switch /></Form.Item>
      </Form>
    </Modal>
  )
}

export default function Alerts() {
  const { message } = AntApp.useApp()
  const canManage = useCan('alerts.manage')
  const [state, setState] = useState<'open' | ''>('open')
  const alerts = useFetch<{ items: AlertRow[]; open: Record<string, number> }>(`/alerts?state=${state}`)
  const rules = useFetch<{ items: Rule[] }>('/alert-rules')
  const channels = useFetch<{ items: Channel[] }>(canManage ? '/channels' : null)
  const groups = useFetch<{ items: Group[] }>('/groups')
  const [rule, setRule] = useState<Partial<Rule> | null>(null)
  const [channel, setChannel] = useState<Partial<Channel> | null>(null)
  const open = alerts.data?.open ?? {}

  const ack = async (a: AlertRow) => {
    try {
      await api.post(`/alerts/${a.id}/ack`)
      void alerts.reload()
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  return (
    <>
      <PageHeader title="هشدارها" subtitle={`${faNum(open.critical ?? 0)} بحرانی · ${faNum(open.warning ?? 0)} هشدار · ${faNum(open.info ?? 0)} اطلاع (باز)`} />
      <Tabs items={[
        {
          key: 'alerts', label: 'هشدارها', children: (
            <Card extra={<Radio.Group size="small" optionType="button" value={state} onChange={(e) => setState(e.target.value)} options={[{ value: 'open', label: 'باز' }, { value: '', label: 'همه' }]} />}>
              <Table<AlertRow> rowKey="id" size="small" loading={alerts.loading} dataSource={alerts.data?.items ?? []} scroll={{ x: 900 }}
                pagination={{ pageSize: 25, hideOnSinglePage: true }} locale={{ emptyText: state === 'open' ? 'هشدار بازی نیست' : 'هشداری ثبت نشده' }}
                columns={[
                  { title: 'شدت', render: (_, a) => severityTag(a.severity), width: 80 },
                  { title: 'دستگاه', render: (_, a) => <Link to={`/devices/${a.device_id}`}>{a.device_name}</Link> },
                  { title: 'قانون', dataIndex: 'rule_name' },
                  { title: 'پیام', dataIndex: 'message' },
                  { title: 'شروع', render: (_, a) => faDateTime(a.opened_at) },
                  {
                    title: 'وضعیت', render: (_, a) =>
                      a.state === 'resolved' ? <Tag>رفع‌شده {faDateTime(a.resolved_at)}</Tag>
                        : a.acked_at ? <Tag color="blue">دیده‌شده توسط {a.acked_by}</Tag>
                          : <Button size="small" onClick={() => void ack(a)}>تأیید دیدن</Button>,
                  },
                ]} />
            </Card>
          ),
        },
        {
          key: 'rules', label: 'قوانین', children: (
            <Card extra={canManage && <Button type="primary" onClick={() => setRule({})}>+ قانون جدید</Button>}>
              <Table<Rule> rowKey="id" size="small" pagination={false} dataSource={rules.data?.items ?? []} scroll={{ x: 800 }}
                columns={[
                  { title: 'نام', dataIndex: 'name' },
                  { title: 'شرط', render: (_, r) => ruleKinds[r.kind]?.label ?? r.kind },
                  { title: 'آستانه', render: (_, r) => (r.kind === 'offline' ? '—' : `${faNum(r.threshold)} ${ruleKinds[r.kind]?.unit ?? ''}`) },
                  { title: 'مدت', render: (_, r) => (r.duration_minutes ? `${faNum(r.duration_minutes)} دقیقه` : 'فوری') },
                  { title: 'شدت', render: (_, r) => severityTag(r.severity) },
                  { title: 'کانال‌ها', render: (_, r) => (r.channel_ids.length ? faNum(r.channel_ids.length) : <Typography.Text type="secondary">فقط پنل</Typography.Text>) },
                  { title: 'وضعیت', render: (_, r) => (r.enabled ? <Tag color="green">فعال</Tag> : <Tag>غیرفعال</Tag>) },
                  ...(canManage ? [{
                    title: '', render: (_: unknown, r: Rule) => (
                      <Space>
                        <Button size="small" type="link" onClick={() => setRule(r)}>ویرایش</Button>
                        <Popconfirm title="قانون و هشدارهای آن حذف شوند؟" okText="حذف" cancelText="انصراف"
                          onConfirm={async () => { await api.del(`/alert-rules/${r.id}`).catch((e) => message.error(e.message)); void rules.reload() }}>
                          <Button size="small" type="link" danger>حذف</Button>
                        </Popconfirm>
                      </Space>
                    ),
                  }] : []),
                ]} />
            </Card>
          ),
        },
        ...(canManage ? [{
          key: 'channels', label: 'کانال‌های اعلان', children: (
            <Card extra={<Button type="primary" onClick={() => setChannel({})}>+ کانال جدید</Button>}>
              <Typography.Paragraph type="secondary">اعلان پیامکی (sms.ir) و پیام‌رسان‌های بله/ایتا در فازهای بعد اضافه می‌شوند.</Typography.Paragraph>
              <Table<Channel> rowKey="id" size="small" pagination={false} dataSource={channels.data?.items ?? []}
                locale={{ emptyText: 'کانالی تعریف نشده' }}
                columns={[
                  { title: 'نام', dataIndex: 'name' },
                  { title: 'نوع', render: (_, c) => channelKinds[c.kind] },
                  { title: 'مقصد', render: (_, c) => <span dir="ltr">{c.kind === 'email' ? c.config.to?.join(', ') : c.kind === 'webhook' ? c.config.url : c.config.address}</span> },
                  { title: 'وضعیت', render: (_, c) => (c.enabled ? <Tag color="green">فعال</Tag> : <Tag>غیرفعال</Tag>) },
                  {
                    title: '', render: (_, c) => (
                      <Space>
                        <Button size="small" type="link" onClick={async () => {
                          try { await api.post(`/channels/${c.id}/test`); message.success('پیام آزمایشی ارسال شد') } catch (e) { message.error((e as Error).message) }
                        }}>ارسال آزمایشی</Button>
                        <Button size="small" type="link" onClick={() => setChannel(c)}>ویرایش</Button>
                        <Popconfirm title="کانال حذف شود؟" okText="حذف" cancelText="انصراف"
                          onConfirm={async () => { await api.del(`/channels/${c.id}`).catch((e) => message.error(e.message)); void channels.reload() }}>
                          <Button size="small" type="link" danger>حذف</Button>
                        </Popconfirm>
                      </Space>
                    ),
                  },
                ]} />
            </Card>
          ),
        }] : []),
      ]} />
      <RuleModal rule={rule} groups={groups.data?.items ?? []} channels={channels.data?.items ?? []} onClose={(s) => { setRule(null); if (s) void rules.reload() }} />
      <ChannelModal channel={channel} onClose={(s) => { setChannel(null); if (s) void channels.reload() }} />
    </>
  )
}

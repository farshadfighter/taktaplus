import { useEffect, useState } from 'react'
import { Alert, Button, Card, Checkbox, Col, DatePicker, Form, Input, InputNumber, Modal, Popconfirm, Radio, Row, Select, Space, Switch, Table, Tag, TimePicker, Typography, App as AntApp } from 'antd'
import dayjs, { type Dayjs } from 'dayjs'
import { useSearchParams } from 'react-router'
import { api, type Channel, type Group, type LogCatalog, type ReportRun, type ReportSchedule, type ReportTemplate } from '../api'
import { useCan } from '../auth'
import { faDateTime, faNum } from '../format'
import { DeviceSelect, useDevices } from '../logui'
import { PageHeader, bytesText } from '../ui'
import { useFetch } from '../useFetch'

const weekdays = ['یکشنبه', 'دوشنبه', 'سه‌شنبه', 'چهارشنبه', 'پنجشنبه', 'جمعه', 'شنبه']
const hhmm = (m: number) => faNum(Math.floor(m / 60)).padStart(2, '۰') + ':' + faNum(m % 60).padStart(2, '۰')

function repeatText(s: ReportSchedule) {
  if (s.frequency === 'daily') return `روزانه ${hhmm(s.at_minute)}`
  if (s.frequency === 'weekly') return `هفتگی · ${weekdays[s.weekday]} ${hhmm(s.at_minute)}`
  return `ماهانه · روز ${faNum(s.monthday)} ${hhmm(s.at_minute)}`
}

const runTag = (r: ReportRun) =>
  ({
    running: <Tag color="processing">در حال تولید</Tag>,
    ready: <Tag color="blue">آماده</Tag>,
    sent: <Tag color="green">ارسال شد</Tag>,
    failed: <Tag color="red" title={r.error}>ناموفق</Tag>,
  })[r.status]

function RunModal({ template, initial, onClose, onStarted }: {
  template: ReportTemplate | null
  initial?: { from?: string; to?: string; devices?: number[] }
  onClose: () => void
  onStarted: () => void
}) {
  const { message } = AntApp.useApp()
  const { list } = useDevices()
  const [form] = Form.useForm()
  useEffect(() => {
    if (!template) return
    form.setFieldsValue({
      range: [initial?.from ? dayjs(initial.from) : dayjs().subtract(7, 'day').startOf('day'), initial?.to ? dayjs(initial.to) : dayjs().startOf('day')],
      devices: initial?.devices ?? [],
      formats: ['pdf'],
    })
  }, [template, initial, form])
  return (
    <Modal open={template !== null} title={`اجرای گزارش: ${template?.name ?? ''}`} okText="تولید گزارش" cancelText="انصراف" onCancel={onClose} onOk={() => form.submit()} destroyOnHidden>
      <Form form={form} layout="vertical" requiredMark={false} onFinish={async (v) => {
        const [a, b] = v.range as [Dayjs, Dayjs]
        try {
          await api.post('/reports/runs', { template_id: template!.id, from: a.toISOString(), to: b.toISOString(), device_ids: v.devices, formats: v.formats })
          message.success('گزارش در حال تولید است')
          onStarted()
          onClose()
        } catch (e) {
          message.error((e as Error).message)
        }
      }}>
        <Form.Item name="range" label="بازه" rules={[{ required: true }]}>
          <DatePicker.RangePicker showTime={{ format: 'HH:mm' }} format="YYYY-MM-DD HH:mm" style={{ width: '100%' }} />
        </Form.Item>
        <Form.Item name="devices" label="دستگاه‌ها"><DeviceSelect devices={list} style={{ width: '100%' }} /></Form.Item>
        <Form.Item name="formats" label="خروجی" rules={[{ required: true, message: 'حداقل یک خروجی' }]}>
          <Checkbox.Group options={[{ value: 'pdf', label: 'PDF' }, { value: 'xlsx', label: 'Excel' }]} />
        </Form.Item>
      </Form>
    </Modal>
  )
}

function TemplateModal({ open, value, catalog, onClose, onSaved }: { open: boolean; value: ReportTemplate | null; catalog: LogCatalog | null; onClose: () => void; onSaved: () => void }) {
  const { message } = AntApp.useApp()
  const [form] = Form.useForm()
  useEffect(() => {
    if (open) form.setFieldsValue(value ? { name: value.builtin ? value.name + ' (کپی)' : value.name, description: value.description, widgets: value.widgets } : { name: '', description: '', widgets: [] })
  }, [open, value, form])
  const groups: Record<string, string> = { security: 'امنیت', traffic: 'ترافیک', web: 'WebFilter', vpn: 'VPN', waf: 'WAF', events: 'رویدادها' }
  const editing = value && !value.builtin
  return (
    <Modal open={open} title={editing ? 'ویرایش قالب' : 'قالب سفارشی'} okText="ذخیره" cancelText="انصراف" onCancel={onClose} onOk={() => form.submit()} width={640} destroyOnHidden>
      <Form form={form} layout="vertical" requiredMark={false} onFinish={async (v) => {
        try {
          if (editing) await api.put(`/reports/templates/${value.id}`, v)
          else await api.post('/reports/templates', v)
          message.success('ذخیره شد')
          onSaved()
          onClose()
        } catch (e) {
          message.error((e as Error).message)
        }
      }}>
        <Form.Item name="name" label="نام" rules={[{ required: true }]}><Input /></Form.Item>
        <Form.Item name="description" label="توضیح"><Input /></Form.Item>
        <Form.Item name="widgets" label="بخش‌ها (به ترتیب انتخاب)" rules={[{ required: true, message: 'حداقل یک بخش' }]}>
          <Select mode="multiple" optionFilterProp="label" options={Object.entries(groups).map(([g, label]) => ({
            label, options: (catalog?.widgets ?? []).filter((w) => w.group === g).map((w) => ({ value: w.id, label: w.title })),
          }))} />
        </Form.Item>
      </Form>
    </Modal>
  )
}

function ScheduleModal({ open, value, templates, onClose, onSaved }: { open: boolean; value: Partial<ReportSchedule> | null; templates: ReportTemplate[]; onClose: () => void; onSaved: () => void }) {
  const { message } = AntApp.useApp()
  const [form] = Form.useForm()
  const { list } = useDevices()
  const groups = useFetch<{ items: Group[] }>('/groups')
  const channels = useFetch<{ items: Channel[] }>('/reports/channels')
  const freq = Form.useWatch('frequency', form)
  const scope = Form.useWatch('scope', form)
  useEffect(() => {
    if (!open) return
    const v = value ?? {}
    form.setFieldsValue({
      template_id: v.template_id, frequency: v.frequency ?? 'weekly', weekday: v.weekday ?? 6, monthday: v.monthday ?? 1,
      at: dayjs().startOf('day').add(v.at_minute ?? 420, 'minute'), scope: v.group_id ? 'group' : 'devices',
      group_id: v.group_id ?? null, device_ids: v.device_ids ?? [], formats: v.formats ?? ['pdf'], channel_id: v.channel_id ?? null,
      recipients: (v.recipients ?? []).join('\n'), enabled: v.enabled ?? true,
    })
  }, [open, value, form])
  return (
    <Modal open={open} title={value?.id ? 'ویرایش زمان‌بندی' : 'زمان‌بندی گزارش'} okText="ذخیره" cancelText="انصراف" onCancel={onClose} onOk={() => form.submit()} width={620} destroyOnHidden>
      <Form form={form} layout="vertical" requiredMark={false} onFinish={async (v) => {
        const at = v.at as Dayjs
        const body = {
          template_id: v.template_id, frequency: v.frequency, weekday: v.weekday, monthday: v.monthday, at_minute: at.hour() * 60 + at.minute(),
          group_id: v.scope === 'group' ? v.group_id : null, device_ids: v.scope === 'devices' ? v.device_ids : [], formats: v.formats,
          channel_id: v.channel_id ?? null, recipients: String(v.recipients ?? '').split(/[\s,;]+/).filter(Boolean), enabled: v.enabled,
        }
        try {
          if (value?.id) await api.put(`/reports/schedules/${value.id}`, body)
          else await api.post('/reports/schedules', body)
          message.success('ذخیره شد')
          onSaved()
          onClose()
        } catch (e) {
          message.error((e as Error).message)
        }
      }}>
        <Form.Item name="template_id" label="گزارش" rules={[{ required: true }]}>
          <Select options={templates.map((t) => ({ value: t.id, label: t.name }))} />
        </Form.Item>
        <Row gutter={12}>
          <Col span={8}>
            <Form.Item name="frequency" label="تکرار">
              <Select options={[{ value: 'daily', label: 'روزانه' }, { value: 'weekly', label: 'هفتگی' }, { value: 'monthly', label: 'ماهانه (شمسی)' }]} />
            </Form.Item>
          </Col>
          <Col span={8}>
            {freq === 'weekly' && <Form.Item name="weekday" label="روز"><Select options={[6, 0, 1, 2, 3, 4, 5].map((d) => ({ value: d, label: weekdays[d] }))} /></Form.Item>}
            {freq === 'monthly' && <Form.Item name="monthday" label="روز ماه"><InputNumber min={1} max={29} style={{ width: '100%' }} /></Form.Item>}
          </Col>
          <Col span={8}><Form.Item name="at" label="ساعت"><TimePicker format="HH:mm" minuteStep={5} style={{ width: '100%' }} /></Form.Item></Col>
        </Row>
        <Typography.Paragraph type="secondary" style={{ marginTop: -8 }}>
          {freq === 'daily' ? 'گزارش روز قبل' : freq === 'weekly' ? 'گزارش هفت روز گذشته' : 'گزارش ماه شمسی قبل'} تهیه می‌شود.
        </Typography.Paragraph>
        <Form.Item name="scope" label="دامنه">
          <Radio.Group options={[{ value: 'devices', label: 'دستگاه‌ها' }, { value: 'group', label: 'گروه' }]} />
        </Form.Item>
        {scope === 'group' ? (
          <Form.Item name="group_id" rules={[{ required: true, message: 'گروه را انتخاب کنید' }]}><Select options={(groups.data?.items ?? []).map((g) => ({ value: g.id, label: g.name }))} /></Form.Item>
        ) : (
          <Form.Item name="device_ids"><DeviceSelect devices={list} style={{ width: '100%' }} /></Form.Item>
        )}
        <Form.Item name="formats" label="خروجی" rules={[{ required: true }]}>
          <Checkbox.Group options={[{ value: 'pdf', label: 'PDF' }, { value: 'xlsx', label: 'Excel' }]} />
        </Form.Item>
        <Row gutter={12}>
          <Col span={10}>
            <Form.Item name="channel_id" label="ارسال با کانال ایمیل" extra={!channels.data?.items.length ? 'کانال ایمیل را در «هشدارها» بسازید.' : undefined}>
              <Select allowClear placeholder="بدون ارسال ایمیل" options={(channels.data?.items ?? []).map((c) => ({ value: c.id, label: c.name }))} />
            </Form.Item>
          </Col>
          <Col span={14}>
            <Form.Item name="recipients" label="گیرندگان (خالی = گیرندگان کانال)">
              <Input.TextArea rows={2} dir="ltr" placeholder="ciso@example.com" />
            </Form.Item>
          </Col>
        </Row>
        <Form.Item name="enabled" label="فعال" valuePropName="checked"><Switch /></Form.Item>
      </Form>
    </Modal>
  )
}

export default function Reports() {
  const { message } = AntApp.useApp()
  const [params, setParams] = useSearchParams()
  const canManage = useCan('reports.manage')
  const templates = useFetch<{ items: ReportTemplate[] }>('/reports/templates')
  const schedules = useFetch<{ items: ReportSchedule[]; timezone: string }>('/reports/schedules')
  const runs = useFetch<{ items: ReportRun[] }>('/reports/runs?limit=50')
  const catalog = useFetch<LogCatalog>('/logs/catalog')
  const { names } = useDevices()
  const [runTpl, setRunTpl] = useState<ReportTemplate | null>(null)
  const [runInit, setRunInit] = useState<{ from?: string; to?: string; devices?: number[] } | undefined>()
  const [tplEdit, setTplEdit] = useState<{ open: boolean; value: ReportTemplate | null }>({ open: false, value: null })
  const [schedEdit, setSchedEdit] = useState<{ open: boolean; value: Partial<ReportSchedule> | null }>({ open: false, value: null })
  const tpls = templates.data?.items ?? []

  // Opened from the analytics page with a template and period.
  useEffect(() => {
    const id = Number(params.get('run'))
    const t = tpls.find((x) => x.id === id)
    if (t) {
      setRunInit({ from: params.get('from') ?? undefined, to: params.get('to') ?? undefined, devices: (params.get('devices') ?? '').split(',').filter(Boolean).map(Number) })
      setRunTpl(t)
      setParams({}, { replace: true })
    }
  }, [params, tpls, setParams])

  // Refresh while reports are being generated.
  const busy = runs.data?.items.some((r) => r.status === 'running')
  useEffect(() => {
    if (!busy) return
    const t = setInterval(() => void runs.reload(), 2000)
    return () => clearInterval(t)
  }, [busy, runs])

  const act = async (fn: () => Promise<unknown>, ok: string) => {
    try {
      await fn()
      message.success(ok)
    } catch (e) {
      message.error((e as Error).message)
    }
  }
  const widgetTitle = (id: string) => catalog.data?.widgets.find((w) => w.id === id)?.title ?? id

  return (
    <>
      <PageHeader
        title="گزارش‌ها"
        subtitle="قالب‌های آماده و سفارشی، زمان‌بندی و ارسال ایمیلی با خروجی PDF و Excel"
        extra={canManage && <Button type="primary" onClick={() => setTplEdit({ open: true, value: null })}>+ قالب سفارشی</Button>}
      />
      <Typography.Title level={5}>قالب‌ها</Typography.Title>
      <Row gutter={[12, 12]} style={{ marginBottom: 24 }}>
        {tpls.map((t) => (
          <Col xs={24} md={12} xl={8} key={t.id}>
            <Card size="small" style={{ height: '100%' }} styles={{ body: { display: 'flex', flexDirection: 'column', height: '100%', gap: 8 } }}>
              <Space>
                <Typography.Text strong>{t.name}</Typography.Text>
                <Tag>{t.builtin ? 'آماده' : 'سفارشی'}</Tag>
              </Space>
              <Typography.Text type="secondary" style={{ fontSize: 12, flex: 1 }}>{t.description || t.widgets.map(widgetTitle).join('، ')}</Typography.Text>
              <Space wrap>
                <Button size="small" type="primary" ghost onClick={() => { setRunInit(undefined); setRunTpl(t) }}>اجرا</Button>
                {canManage && <Button size="small" onClick={() => setSchedEdit({ open: true, value: { template_id: t.id } })}>زمان‌بندی</Button>}
                {canManage && <Button size="small" type="link" onClick={() => setTplEdit({ open: true, value: t })}>{t.builtin ? 'کپی و ویرایش' : 'ویرایش'}</Button>}
                {canManage && !t.builtin && (
                  <Popconfirm title="قالب و زمان‌بندی‌هایش حذف شوند؟" okText="حذف" cancelText="انصراف" onConfirm={() => act(async () => { await api.del(`/reports/templates/${t.id}`); await templates.reload(); await schedules.reload() }, 'حذف شد')}>
                    <Button size="small" type="link" danger>حذف</Button>
                  </Popconfirm>
                )}
              </Space>
            </Card>
          </Col>
        ))}
      </Row>

      <Card title="زمان‌بندی‌ها" size="small" style={{ marginBottom: 24 }} styles={{ body: { padding: 0 } }}
        extra={schedules.data && <Typography.Text type="secondary" style={{ fontSize: 12 }}>منطقه‌ی زمانی: {schedules.data.timezone}</Typography.Text>}>
        <Table<ReportSchedule>
          size="small"
          rowKey="id"
          pagination={false}
          dataSource={schedules.data?.items ?? []}
          locale={{ emptyText: 'زمان‌بندی‌ای تعریف نشده است' }}
          scroll={{ x: true }}
          columns={[
            { title: 'گزارش', dataIndex: 'template' },
            { title: 'دامنه', render: (_, s) => (s.group_id ? 'گروه' : s.device_ids.length ? s.device_ids.map((id) => names.get(id) ?? id).join('، ') : 'همه') },
            { title: 'تکرار', render: (_, s) => repeatText(s) },
            { title: 'خروجی', dataIndex: 'formats', render: (f: string[]) => f.map((x) => x.toUpperCase()).join(' + ') },
            { title: 'گیرندگان', render: (_, s) => (s.channel_id ? (s.recipients.length ? <span dir="ltr">{s.recipients.join(', ')}</span> : 'گیرندگان کانال') : '—') },
            { title: 'اجرای بعدی', dataIndex: 'next_run_at', render: (v: string, s) => (s.enabled ? faDateTime(v) : '—') },
            {
              title: 'فعال', dataIndex: 'enabled',
              render: (v: boolean, s) => <Switch size="small" checked={v} disabled={!canManage} onChange={(on) => act(async () => { await api.put(`/reports/schedules/${s.id}`, { ...s, enabled: on }); await schedules.reload() }, on ? 'فعال شد' : 'غیرفعال شد')} />,
            },
            ...(canManage ? [{
              title: '', key: 'a', render: (_: unknown, s: ReportSchedule) => (
                <Space>
                  <Button size="small" type="link" onClick={() => setSchedEdit({ open: true, value: s })}>ویرایش</Button>
                  <Popconfirm title="زمان‌بندی حذف شود؟" okText="حذف" cancelText="انصراف" onConfirm={() => act(async () => { await api.del(`/reports/schedules/${s.id}`); await schedules.reload() }, 'حذف شد')}>
                    <Button size="small" type="link" danger>حذف</Button>
                  </Popconfirm>
                </Space>
              ),
            }] : []),
          ]}
        />
      </Card>

      <Card title="آخرین گزارش‌های تولیدشده" size="small" styles={{ body: { padding: 0 } }}>
        <Table<ReportRun>
          size="small"
          rowKey="id"
          pagination={false}
          dataSource={runs.data?.items ?? []}
          locale={{ emptyText: 'هنوز گزارشی تولید نشده است' }}
          scroll={{ x: true }}
          columns={[
            { title: 'گزارش', render: (_, r) => <>{r.title}{!r.schedule_id && <Typography.Text type="secondary"> (دستی)</Typography.Text>}</> },
            { title: 'بازه', render: (_, r) => <span style={{ whiteSpace: 'nowrap' }}>{faDateTime(r.period_from)} تا {faDateTime(r.period_to)}</span> },
            { title: 'تولید', dataIndex: 'created_at', render: (v: string) => faDateTime(v) },
            { title: 'وضعیت', render: (_, r) => <Space orientation="vertical" size={0}>{runTag(r)}{r.status === 'failed' && <Typography.Text type="danger" style={{ fontSize: 12 }}>{r.error}</Typography.Text>}</Space> },
            {
              title: 'فایل‌ها', render: (_, r) => (
                <Space>
                  {r.files.map((f) => (
                    <Button key={f.format} size="small" href={`/api/v1/reports/runs/${r.id}/files/${f.format}`} title={bytesText(f.size)}>{f.format === 'pdf' ? 'PDF' : 'Excel'}</Button>
                  ))}
                </Space>
              ),
            },
            ...(canManage ? [{
              title: '', key: 'a', render: (_: unknown, r: ReportRun) => (
                <Space>
                  {r.status === 'failed' && <Button size="small" type="link" onClick={() => act(async () => { await api.post(`/reports/runs/${r.id}/retry`); await runs.reload() }, 'دوباره در حال تولید است')}>تلاش مجدد</Button>}
                  {r.status !== 'running' && (
                    <Popconfirm title="گزارش و فایل‌هایش حذف شوند؟" okText="حذف" cancelText="انصراف" onConfirm={() => act(async () => { await api.del(`/reports/runs/${r.id}`); await runs.reload() }, 'حذف شد')}>
                      <Button size="small" type="link" danger>حذف</Button>
                    </Popconfirm>
                  )}
                </Space>
              ),
            }] : []),
          ]}
        />
      </Card>
      {catalog.error && <Alert type="warning" showIcon title={catalog.error} style={{ marginTop: 16 }} />}

      <RunModal template={runTpl} initial={runInit} onClose={() => setRunTpl(null)} onStarted={() => void runs.reload()} />
      <TemplateModal open={tplEdit.open} value={tplEdit.value} catalog={catalog.data} onClose={() => setTplEdit({ open: false, value: null })} onSaved={() => void templates.reload()} />
      <ScheduleModal open={schedEdit.open} value={schedEdit.value} templates={tpls} onClose={() => setSchedEdit({ open: false, value: null })} onSaved={() => void schedules.reload()} />
    </>
  )
}

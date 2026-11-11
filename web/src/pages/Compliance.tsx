import { useState } from 'react'
import { Alert, Button, Card, Col, Drawer, Empty, Form, Input, Modal, Popconfirm, Progress, Radio, Row, Select, Space, Statistic, Switch, Table, Tabs, Tag, Typography, App as AntApp } from 'antd'
import { useNavigate, useSearchParams } from 'react-router'
import { api, type ComplianceException, type ComplianceResult, type ComplianceRule, type ComplianceTemplate, type Cond, type Group } from '../api'
import { useCan } from '../auth'
import { faDateTime, faNum } from '../format'
import { Mono, PageHeader, kindLabel } from '../ui'
import { useFetch } from '../useFetch'

const sevTag: Record<string, [string, string]> = { critical: ['بحرانی', 'red'], high: ['بالا', 'volcano'], medium: ['متوسط', 'orange'], low: ['کم', 'default'] }
const opNames: Record<Cond['op'], string> = { eq: '=', ne: '≠', contains: 'شامل', not_contains: 'شامل نباشد', gte: '≥', lte: '≤', set: 'تنظیم شده', unset: 'تنظیم نشده' }
const condText = (c: Cond) => `${c.key} ${opNames[c.op]}${c.op === 'set' || c.op === 'unset' ? '' : ' ' + c.value}${c.default ? ` (پیش‌فرض ${c.default})` : ''}`
const scoreColor = (s: number) => (s >= 90 ? '#2E7D32' : s >= 70 ? '#D48806' : '#CF1322')

type Overview = {
  items: ComplianceResult[]
  summary: { average: number; drifting: number; broken_rules: number; last_evaluated: string | null }
}

// DeviceDrawer shows a device's drift and findings with fix and exception
// actions.
function DeviceDrawer({ deviceID, onClose, onChanged }: { deviceID: number | null; onClose: () => void; onChanged: () => void }) {
  const { message } = AntApp.useApp()
  const nav = useNavigate()
  const canManage = useCan('compliance.manage')
  const canChange = useCan('changes.create')
  const data = useFetch<{ result: ComplianceResult; exceptions: ComplianceException[] }>(deviceID ? `/compliance/devices/${deviceID}` : null)
  const [except, setExcept] = useState<{ ref: string; label: string } | null>(null)
  const [reason, setReason] = useState('')
  const r = data.data?.result
  const reload = () => { void data.reload(); onChanged() }
  return (
    <Drawer open={deviceID !== null} onClose={onClose} size={760} title={r ? `${r.device_name} · امتیاز ${faNum(r.score)}` : 'انطباق'} destroyOnHidden>
      {r?.error && <Alert type="warning" showIcon title={r.error} style={{ marginBottom: 12 }} />}
      {r && (
        <>
          <Typography.Paragraph type="secondary">
            ارزیابی: {faDateTime(r.evaluated_at)} · قالب‌ها: {r.templates.length ? r.templates.join('، ') : 'هیچ'}
          </Typography.Paragraph>
          <Typography.Title level={5}>انحراف از قالب‌ها</Typography.Title>
          {r.drift.length === 0 && <Typography.Paragraph type="secondary">انحرافی نیست.</Typography.Paragraph>}
          {r.drift.map((d) => (
            <Card key={d.template_id} size="small" style={{ marginBottom: 12 }}
              title={<>انحراف از «{d.template}»</>}
              extra={canChange && !d.error && (
                <Button size="small" type="primary" onClick={async () => {
                  try {
                    const c = await api.post<{ id: number }>(`/compliance/devices/${r.device_id}/fix`, { template_id: d.template_id })
                    message.success('درخواست تغییر برای اصلاح ثبت شد')
                    nav(`/changes/${c.id}`)
                  } catch (e) {
                    message.error((e as Error).message)
                  }
                }}>اصلاح (درخواست تغییر)</Button>
              )}>
              {d.error && <Alert type="error" showIcon title={d.error} />}
              {d.differences.map((x, i) => {
                const ref = `drift:${d.template_id}:${x.path}|${x.key ?? ''}`
                return (
                  <div key={i} style={{ borderBottom: '1px solid #F0F0F0', padding: '8px 0', opacity: x.excepted ? 0.5 : 1 }}>
                    <div style={{ display: 'flex', justifyContent: 'space-between', gap: 8 }}>
                      <Mono>{x.path}{x.key ? ` › ${x.key}` : ''}</Mono>
                      <Space size={4}>
                        {x.excepted && <Tag>استثنا</Tag>}
                        {canManage && !x.excepted && <Button size="small" type="link" onClick={() => { setReason(''); setExcept({ ref, label: `${x.path} ${x.key ?? ''}` }) }}>پذیرفتن به‌عنوان استثنا</Button>}
                      </Space>
                    </div>
                    <pre dir="ltr" style={{ margin: '6px 0 0', fontSize: 12, background: '#FAFAFA', padding: 8, borderRadius: 6, whiteSpace: 'pre-wrap' }}>
                      {x.missing ? <span style={{ color: '#CF1322' }}>- missing: {x.expected || '(entry)'}</span> : (
                        <>
                          <span style={{ color: '#CF1322' }}>- set {x.key} {x.expected || '(unset)'}</span>{'\n'}
                          <span style={{ color: '#2E7D32' }}>+ set {x.key} {x.actual || '(unset)'}</span>
                        </>
                      )}
                    </pre>
                  </div>
                )
              })}
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>قرمز = مقدار قالب، سبز = مقدار فعلی دستگاه</Typography.Text>
            </Card>
          ))}
          <Typography.Title level={5} style={{ marginTop: 16 }}>قوانین نقض‌شده</Typography.Title>
          <Table size="small" rowKey={(f) => `${f.rule_id}-${f.entry}`} pagination={false} dataSource={r.violations} locale={{ emptyText: 'همه‌ی قوانین رعایت شده‌اند' }}
            columns={[
              { title: 'قانون', dataIndex: 'rule' },
              { title: 'شدت', dataIndex: 'severity', render: (v: string) => <Tag color={sevTag[v]?.[1]}>{sevTag[v]?.[0] ?? v}</Tag> },
              { title: 'مورد', dataIndex: 'entry', render: (v: string) => (v ? <Mono>{v}</Mono> : '—') },
              { title: 'جزئیات', dataIndex: 'detail', render: (v: string) => <span style={{ fontSize: 12 }}>{v}</span> },
              {
                title: '', key: 'a',
                render: (_: unknown, f) => (f.excepted ? <Tag>استثنا</Tag> : canManage && (
                  <Button size="small" type="link" onClick={() => { setReason(''); setExcept({ ref: `rule:${f.rule_id}:${f.entry}`, label: `${f.rule} ${f.entry}` }) }}>استثنا</Button>
                )),
              },
            ]} />
          {(data.data?.exceptions.length ?? 0) > 0 && (
            <>
              <Typography.Title level={5} style={{ marginTop: 16 }}>استثناها</Typography.Title>
              <Table size="small" rowKey="id" pagination={false} dataSource={data.data?.exceptions}
                columns={[
                  { title: 'مورد', dataIndex: 'ref', render: (v: string) => <Mono>{v}</Mono> },
                  { title: 'دلیل', dataIndex: 'reason' },
                  { title: 'ثبت', render: (_, e) => `${e.created_by} · ${faDateTime(e.created_at)}` },
                  ...(canManage ? [{
                    title: '', key: 'a',
                    render: (_: unknown, e: ComplianceException) => (
                      <Popconfirm title="استثنا حذف شود؟" okText="حذف" cancelText="انصراف" onConfirm={async () => { await api.del(`/compliance/exceptions/${e.id}`); reload() }}>
                        <Button size="small" type="link" danger>حذف</Button>
                      </Popconfirm>
                    ),
                  }] : []),
                ]} />
            </>
          )}
        </>
      )}
      <Modal open={except !== null} title="پذیرفتن به‌عنوان استثنا" okText="ثبت" cancelText="انصراف" okButtonProps={{ disabled: !reason.trim() }}
        onCancel={() => setExcept(null)} onOk={async () => {
          try {
            await api.post(`/compliance/devices/${deviceID}/exceptions`, { ref: except!.ref, reason })
            setExcept(null)
            reload()
          } catch (e) {
            message.error((e as Error).message)
          }
        }}>
        <Typography.Paragraph><Mono>{except?.label}</Mono></Typography.Paragraph>
        <Input.TextArea rows={3} placeholder="دلیل (مثلاً: تأییدشده توسط تیم امنیت)" value={reason} onChange={(e) => setReason(e.target.value)} />
      </Modal>
    </Drawer>
  )
}

function TemplateModal({ value, groups, onClose }: { value: Partial<ComplianceTemplate> | null; groups: Group[]; onClose: (saved: boolean) => void }) {
  const { message } = AntApp.useApp()
  const [form] = Form.useForm()
  return (
    <Modal open={value !== null} width={760} title={value?.id ? `ویرایش قالب (نسخه ${faNum(value.version)})` : 'قالب جدید'} okText="ذخیره" cancelText="انصراف"
      onCancel={() => onClose(false)} onOk={() => form.submit()} destroyOnHidden
      afterOpenChange={(o) => { if (o) form.setFieldsValue({ platform: 'fortigate', group_ids: [], auto_fix: false, body: '', ...value }) }}>
      <Form form={form} layout="vertical" requiredMark={false} onFinish={async (v) => {
        try {
          if (value?.id) await api.put(`/compliance/templates/${value.id}`, v)
          else await api.post('/compliance/templates', v)
          message.success('ذخیره شد؛ دستگاه‌ها دوباره ارزیابی می‌شوند')
          onClose(true)
        } catch (e) {
          message.error((e as Error).message)
        }
      }}>
        <Row gutter={12}>
          <Col span={12}><Form.Item name="name" label="نام" rules={[{ required: true }]}><Input /></Form.Item></Col>
          <Col span={12}><Form.Item name="platform" label="پلتفرم"><Radio.Group optionType="button" options={[{ value: 'fortigate', label: 'FortiGate' }, { value: 'fortiweb', label: 'FortiWeb' }]} /></Form.Item></Col>
        </Row>
        <Form.Item name="description" label="توضیح"><Input /></Form.Item>
        <Form.Item name="group_ids" label="گروه‌های متصل" extra="خالی یعنی همه‌ی دستگاه‌های این پلتفرم.">
          <Select mode="multiple" placeholder="همه" options={groups.map((g) => ({ value: g.id, label: g.name }))} />
        </Form.Item>
        <Form.Item name="body" label="بخش‌های کانفیگ" rules={[{ required: true }]}
          extra={<>فقط تنظیماتی که در قالب آمده مقایسه می‌شوند. متغیرها: <Mono>{'{{ .Vars.ntp_server }}'}</Mono> و <Mono>{'{{ .Device.Hostname }}'}</Mono> (از صفحه‌ی اسکریپت‌ها).</>}>
          <Input.TextArea rows={14} dir="ltr" style={{ fontFamily: 'ui-monospace, Consolas, monospace', fontSize: 12 }}
            placeholder={'config system ntp\n    set ntpsync enable\n    set type custom\n    config ntpserver\n        edit 1\n            set server "{{ .Vars.ntp_server }}"\n        next\n    end\nend'} />
        </Form.Item>
        <Form.Item name="auto_fix" label="اصلاح خودکار" valuePropName="checked" extra="با کشف انحراف، درخواست تغییر برای اصلاح ساخته می‌شود؛ اجرا همچنان نیاز به تأیید و پنجره‌ی نگهداری دارد.">
          <Switch />
        </Form.Item>
      </Form>
    </Modal>
  )
}

function CondList({ name, label, extra }: { name: string; label: string; extra?: string }) {
  return (
    <Form.Item label={label} extra={extra}>
      <Form.List name={name}>
        {(fields, { add, remove }) => (
          <>
            {fields.map((f) => (
              <Space key={f.key} align="start" wrap style={{ marginBottom: 4 }}>
                <Form.Item name={[f.name, 'key']} rules={[{ required: true, message: 'تنظیم' }]} noStyle><Input dir="ltr" placeholder="allowaccess" style={{ width: 150 }} /></Form.Item>
                <Form.Item name={[f.name, 'op']} initialValue="eq" noStyle>
                  <Select style={{ width: 120 }} options={Object.entries(opNames).map(([value, label]) => ({ value, label }))} />
                </Form.Item>
                <Form.Item name={[f.name, 'value']} noStyle><Input dir="ltr" placeholder="مقدار" style={{ width: 140 }} /></Form.Item>
                <Form.Item name={[f.name, 'default']} noStyle><Input dir="ltr" placeholder="پیش‌فرض FortiOS" style={{ width: 130 }} /></Form.Item>
                <Button type="link" danger onClick={() => remove(f.name)}>حذف</Button>
              </Space>
            ))}
            <Button size="small" onClick={() => add({ op: 'eq' })}>+ شرط</Button>
          </>
        )}
      </Form.List>
    </Form.Item>
  )
}

function RuleModal({ value, groups, onClose }: { value: Partial<ComplianceRule> | null; groups: Group[]; onClose: (saved: boolean) => void }) {
  const { message } = AntApp.useApp()
  const [form] = Form.useForm()
  const scope = Form.useWatch('scope', form)
  const builtin = !!value?.builtin
  return (
    <Modal open={value !== null} width={760} title={builtin ? 'قانون آماده' : value?.id ? 'ویرایش قانون' : 'قانون سفارشی'} okText="ذخیره" cancelText="انصراف"
      onCancel={() => onClose(false)} onOk={() => form.submit()} destroyOnHidden
      afterOpenChange={(o) => { if (o) form.setFieldsValue({ platform: 'fortigate', severity: 'medium', scope: 'section', match: [], require: [], enabled: true, group_id: null, ...value }) }}>
      <Form form={form} layout="vertical" requiredMark={false} onFinish={async (v) => {
        const body = { ...value, ...v, match: v.match ?? [], require: v.require ?? [], group_id: v.group_id ?? null }
        try {
          if (value?.id) await api.put(`/compliance/rules/${value.id}`, body)
          else await api.post('/compliance/rules', body)
          message.success('ذخیره شد')
          onClose(true)
        } catch (e) {
          message.error((e as Error).message)
        }
      }}>
        <Form.Item name="name" label="نام" rules={[{ required: true }]}><Input disabled={builtin} /></Form.Item>
        <Row gutter={12}>
          <Col span={8}><Form.Item name="severity" label="شدت"><Select options={Object.entries(sevTag).map(([value, [label]]) => ({ value, label }))} /></Form.Item></Col>
          <Col span={8}><Form.Item name="platform" label="پلتفرم"><Select disabled={builtin} options={[{ value: 'fortigate', label: 'FortiGate' }, { value: 'fortiweb', label: 'FortiWeb' }]} /></Form.Item></Col>
          <Col span={8}><Form.Item name="group_id" label="دامنه"><Select allowClear placeholder="همه" options={groups.map((g) => ({ value: g.id, label: g.name }))} /></Form.Item></Col>
        </Row>
        {builtin ? (
          <Alert type="info" showIcon style={{ marginBottom: 12 }} title={<>
            <Mono>config {value?.section}</Mono> — {value?.scope === 'each' ? 'هر مورد' : 'خود بخش'}
            {(value?.match?.length ?? 0) > 0 && <div>موارد: {value!.match!.map(condText).join('، ')}</div>}
            {(value?.require?.length ?? 0) > 0 ? <div>باید: {value!.require!.map(condText).join('، ')}</div> : <div>این موارد ممنوع‌اند.</div>}
          </>} />
        ) : (
          <>
            <Row gutter={12}>
              <Col span={14}><Form.Item name="section" label="بخش کانفیگ" rules={[{ required: true }]} extra="مثلاً system global، firewall policy، system interface">
                <Input dir="ltr" addonBefore="config" /></Form.Item></Col>
              <Col span={10}><Form.Item name="scope" label="بررسی روی">
                <Radio.Group optionType="button" options={[{ value: 'section', label: 'خود بخش' }, { value: 'each', label: 'هر مورد (edit)' }]} /></Form.Item></Col>
            </Row>
            {scope === 'each' && <CondList name="match" label="موارد مشمول (اختیاری)" extra="فقط موردهایی که همه‌ی این شرط‌ها را دارند بررسی می‌شوند." />}
            <CondList name="require" label="باید برقرار باشد" extra={scope === 'each' ? 'اگر خالی بماند، موردهای مشمول ممنوع‌اند (مثل Policy با all/all/ALL).' : undefined} />
          </>
        )}
        <Form.Item name="enabled" label="فعال" valuePropName="checked"><Switch /></Form.Item>
      </Form>
    </Modal>
  )
}

export default function Compliance() {
  const { message } = AntApp.useApp()
  const [params, setParams] = useSearchParams()
  const tab = params.get('tab') ?? 'devices'
  const canManage = useCan('compliance.manage')
  const overview = useFetch<Overview>('/compliance')
  const templates = useFetch<{ items: ComplianceTemplate[] }>('/compliance/templates')
  const rules = useFetch<{ items: ComplianceRule[] }>('/compliance/rules')
  const groups = useFetch<{ items: Group[] }>('/groups')
  const [busy, setBusy] = useState(false)
  const [device, setDevice] = useState<number | null>(null)
  const [tpl, setTpl] = useState<Partial<ComplianceTemplate> | null>(null)
  const [rule, setRule] = useState<Partial<ComplianceRule> | null>(null)
  const sum = overview.data?.summary
  const groupName = (id: number) => groups.data?.items.find((g) => g.id === id)?.name ?? `#${id}`
  const results = overview.data?.items ?? []
  const brokenBy = (id: number) => new Set(results.flatMap((r) => r.violations.filter((v) => v.rule_id === id && !v.excepted).map(() => r.device_id))).size

  const evaluate = async () => {
    setBusy(true)
    try {
      await api.post('/compliance/evaluate')
      await overview.reload()
      message.success('ارزیابی انجام شد')
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const reloadAll = () => { setTimeout(() => void overview.reload(), 1500); void templates.reload(); void rules.reload() }

  return (
    <>
      <PageHeader title="قالب و انطباق" subtitle="قالب‌های سیستمی، کشف انحراف (Drift) و قوانین امنیتی"
        extra={<>
          <Button loading={busy} onClick={evaluate}>ارزیابی همه‌ی دستگاه‌ها</Button>
          {canManage && <Button type="primary" onClick={() => (tab === 'rules' ? setRule({}) : setTpl({}))}>{tab === 'rules' ? '+ قانون' : '+ قالب'}</Button>}
        </>} />
      <Row gutter={[12, 12]} style={{ marginBottom: 16 }}>
        <Col xs={12} md={6}><Card size="small"><Statistic title="میانگین امتیاز انطباق" value={faNum(sum?.average ?? 0)} suffix="/ ۱۰۰" valueStyle={{ color: scoreColor(sum?.average ?? 100) }} /></Card></Col>
        <Col xs={12} md={6}><Card size="small"><Statistic title="دستگاه‌های دارای انحراف" value={faNum(sum?.drifting ?? 0)} /></Card></Col>
        <Col xs={12} md={6}><Card size="small"><Statistic title="قوانین نقض‌شده" value={faNum(sum?.broken_rules ?? 0)} /></Card></Col>
        <Col xs={12} md={6}><Card size="small"><Statistic title="آخرین ارزیابی" value={sum?.last_evaluated ? faDateTime(sum.last_evaluated) : '—'} valueStyle={{ fontSize: 18 }} /></Card></Col>
      </Row>
      <Tabs activeKey={tab} onChange={(k) => setParams({ tab: k })} items={[
        {
          key: 'devices', label: 'دستگاه‌ها', children: (
            <Card styles={{ body: { padding: 0 } }}>
              <Table<ComplianceResult> rowKey="device_id" loading={overview.loading} dataSource={results} pagination={false}
                locale={{ emptyText: <Empty description="هنوز ارزیابی نشده است؛ پس از اولین بکاپ هر دستگاه ارزیابی می‌شود." /> }}
                onRow={(r) => ({ onClick: () => setDevice(r.device_id), style: { cursor: 'pointer' } })}
                columns={[
                  { title: 'دستگاه', render: (_, r) => <>{r.device_name} <Typography.Text type="secondary" style={{ fontSize: 12 }}>{kindLabel(r.kind)}</Typography.Text></> },
                  { title: 'امتیاز', dataIndex: 'score', render: (v: number, r) => (r.error ? <Tag>{r.error}</Tag> : <Progress percent={v} size="small" strokeColor={scoreColor(v)} format={(p) => faNum(p)} style={{ width: 140, margin: 0 }} />) },
                  { title: 'قالب‌ها', dataIndex: 'templates', render: (v: string[]) => (v.length ? v.join('، ') : '—') },
                  { title: 'انحراف', render: (_, r) => (r.drift_templates ? <Tag color="orange">{faNum(r.drift_templates)} قالب · {faNum(r.differences)} مورد</Tag> : '—') },
                  { title: 'قوانین نقض‌شده', dataIndex: 'broken_rules', render: (v: number) => (v ? <Tag color="red">{faNum(v)}</Tag> : '—') },
                  { title: 'ارزیابی', dataIndex: 'evaluated_at', render: faDateTime },
                ]} />
            </Card>
          ),
        },
        {
          key: 'templates', label: 'قالب‌ها', children: (
            <Card styles={{ body: { padding: 0 } }}>
              <Table<ComplianceTemplate> rowKey="id" dataSource={templates.data?.items ?? []} pagination={false} locale={{ emptyText: 'قالبی تعریف نشده است' }}
                columns={[
                  { title: 'قالب', dataIndex: 'name', render: (v, t) => <><b>{v}</b>{t.description && <div style={{ fontSize: 12, color: '#888' }}>{t.description}</div>}</> },
                  { title: 'پلتفرم', dataIndex: 'platform', render: kindLabel },
                  { title: 'بخش‌ها', dataIndex: 'body', render: (b: string) => <Mono>{[...b.matchAll(/^config (.+)$/gm)].map((m) => m[1]).join('، ')}</Mono> },
                  { title: 'گروه‌های متصل', dataIndex: 'group_ids', render: (ids: number[]) => (ids.length ? ids.map(groupName).join('، ') : 'همه') },
                  { title: 'نسخه', dataIndex: 'version', render: (v) => `v${v}` },
                  { title: 'اصلاح خودکار', dataIndex: 'auto_fix', render: (v) => (v ? 'بله، با تأیید' : 'خیر (فقط گزارش)') },
                  ...(canManage ? [{
                    title: '', key: 'a', render: (_: unknown, t: ComplianceTemplate) => (
                      <Space>
                        <Button type="link" onClick={() => setTpl(t)}>ویرایش</Button>
                        <Popconfirm title="قالب حذف شود؟" okText="حذف" cancelText="انصراف" onConfirm={async () => { await api.del(`/compliance/templates/${t.id}`); reloadAll() }}>
                          <Button type="link" danger>حذف</Button>
                        </Popconfirm>
                      </Space>
                    ),
                  }] : []),
                ]} />
            </Card>
          ),
        },
        {
          key: 'rules', label: 'قوانین', children: (
            <Card styles={{ body: { padding: 0 } }}>
              <Table<ComplianceRule> rowKey="id" dataSource={rules.data?.items ?? []} pagination={false}
                columns={[
                  { title: 'قانون', dataIndex: 'name', render: (v, r) => <>{v}{!r.builtin && <Tag style={{ marginInlineStart: 6 }}>سفارشی</Tag>}</> },
                  { title: 'شدت', dataIndex: 'severity', render: (v: string) => <Tag color={sevTag[v][1]}>{sevTag[v][0]}</Tag> },
                  { title: 'پلتفرم', dataIndex: 'platform', render: kindLabel },
                  { title: 'دامنه', dataIndex: 'group_id', render: (v: number | null) => (v ? groupName(v) : 'همه') },
                  { title: 'نتیجه', render: (_, r) => (!r.enabled ? '—' : brokenBy(r.id) ? <Tag color="red">{faNum(brokenBy(r.id))} دستگاه نقض</Tag> : <Tag color="green">همه منطبق</Tag>) },
                  {
                    title: 'فعال', dataIndex: 'enabled',
                    render: (v: boolean, r) => <Switch size="small" checked={v} disabled={!canManage} onChange={async (on) => {
                      try {
                        await api.put(`/compliance/rules/${r.id}`, { ...r, enabled: on })
                        reloadAll()
                      } catch (e) {
                        message.error((e as Error).message)
                      }
                    }} />,
                  },
                  ...(canManage ? [{
                    title: '', key: 'a', render: (_: unknown, r: ComplianceRule) => (
                      <Space>
                        <Button type="link" onClick={() => setRule(r)}>{r.builtin ? 'تنظیم' : 'ویرایش'}</Button>
                        {!r.builtin && (
                          <Popconfirm title="قانون حذف شود؟" okText="حذف" cancelText="انصراف" onConfirm={async () => { await api.del(`/compliance/rules/${r.id}`); reloadAll() }}>
                            <Button type="link" danger>حذف</Button>
                          </Popconfirm>
                        )}
                      </Space>
                    ),
                  }] : []),
                ]} />
            </Card>
          ),
        },
      ]} />
      <DeviceDrawer deviceID={device} onClose={() => setDevice(null)} onChanged={() => void overview.reload()} />
      <TemplateModal value={tpl} groups={groups.data?.items ?? []} onClose={(saved) => { setTpl(null); if (saved) reloadAll() }} />
      <RuleModal value={rule} groups={groups.data?.items ?? []} onClose={(saved) => { setRule(null); if (saved) reloadAll() }} />
    </>
  )
}

import { useMemo, useState } from 'react'
import { Alert, Button, Card, Col, Drawer, Empty, Form, Input, Modal, Popconfirm, Radio, Row, Select, Space, Switch, Table, Tag, Typography, App as AntApp } from 'antd'
import { api, type DeviceRow, type PolicyConflict, type PolicyObject, type PolicyObjectType, type PolicyValue } from '../api'
import { useCan } from '../auth'
import { faNum } from '../format'
import { cleanValue, deviceNumber, objectTypeLabel, ValueFields, valueText } from '../policyui'
import { Mono, PageHeader } from '../ui'
import { useFetch } from '../useFetch'

type Objects = { items: PolicyObject[]; usage: Record<string, string[] | null>; conflicts: PolicyConflict[] | null }
type Kind = 'addr' | 'grp' | 'svc' | 'vip' | 'sched' | 'prof' | 'iface'

const kinds: [Kind, string, PolicyObjectType[]][] = [
  ['addr', 'Address', ['address']],
  ['grp', 'Address Group', ['addrgrp']],
  ['svc', 'Service', ['service', 'servicegrp']],
  ['vip', 'VIP / IP Pool', ['vip', 'ippool']],
  ['sched', 'Schedule', ['schedule']],
  ['prof', 'پروفایل‌های امنیتی', []],
  ['iface', 'نگاشت اینترفیس', []],
]

// ObjectModal creates or edits a shared object, including its per-device
// mappings.
function ObjectModal({ open, obj, defaultType, devices, names, onClose, onSaved }: {
  open: boolean; obj: PolicyObject | null; defaultType: PolicyObjectType; devices: DeviceRow[]
  names: { addresses: string[]; services: string[] }; onClose: () => void; onSaved: () => void
}) {
  const { message } = AntApp.useApp()
  const [form] = Form.useForm()
  const type: PolicyObjectType = Form.useWatch('type', form) ?? obj?.type ?? defaultType
  const [saving, setSaving] = useState(false)
  const fgs = devices.filter((d) => d.kind === 'fortigate')
  return (
    <Modal open={open} title={obj ? `ویرایش ${obj.name}` : 'آبجکت جدید'} onCancel={onClose} width={720} okText="ذخیره" confirmLoading={saving} destroyOnHidden
      onOk={() => form.submit()}
      afterOpenChange={(o) => {
        if (!o) return
        form.resetFields()
        form.setFieldsValue(obj
          ? { ...obj, mappings: Object.entries(obj.mappings ?? {}).map(([d, v]) => ({ device_id: Number(d), value: v })) }
          : { type: defaultType, per_device: false, value: {}, mappings: [] })
      }}>
      <Form form={form} layout="vertical" onFinish={async (v) => {
        setSaving(true)
        try {
          const mappings: Record<string, PolicyValue> = {}
          for (const m of (v.mappings ?? []) as { device_id?: number; value?: PolicyValue }[]) {
            if (m.device_id) mappings[m.device_id] = cleanValue(m.value)
          }
          const body = { type: v.type, name: v.name.trim(), value: cleanValue(v.value), per_device: !!v.per_device, comment: v.comment ?? '', mappings }
          if (obj) await api.put(`/policy/objects/${obj.id}`, { ...body, id: obj.id })
          else await api.post('/policy/objects', { ...body, id: 0 })
          message.success('آبجکت ذخیره شد')
          onSaved()
        } catch (e) {
          message.error((e as Error).message)
        } finally {
          setSaving(false)
        }
      }}>
        <Row gutter={12}>
          <Col span={14}><Form.Item name="name" label="نام" rules={[{ required: true, message: 'نام را وارد کنید' }]}><Input dir="ltr" disabled={!!obj} /></Form.Item></Col>
          <Col span={10}>
            <Form.Item name="type" label="نوع">
              <Select disabled={!!obj} options={Object.entries(objectTypeLabel).map(([value, label]) => ({ value, label }))} />
            </Form.Item>
          </Col>
        </Row>
        <Typography.Text strong>مقدار پیش‌فرض</Typography.Text>
        <div style={{ marginTop: 8 }}><ValueFields type={type} path={['value']} names={names} /></div>
        <Form.Item name="comment" label="توضیح"><Input /></Form.Item>
        {(type === 'address' || type === 'vip' || type === 'ippool') && (
          <>
            <Form.Item name="per_device" valuePropName="checked" label="نگاشت پویا"
              extra="اگر روشن باشد هر دستگاه باید مقدار اختصاصی داشته باشد، وگرنه Install آن دستگاه مسدود می‌شود.">
              <Switch />
            </Form.Item>
            <Typography.Text strong>نگاشت اختصاصی برای هر دستگاه</Typography.Text>
            <Form.List name="mappings">
              {(fields, { add, remove }) => (
                <div style={{ marginTop: 8 }}>
                  {fields.map((f) => (
                    <Card key={f.key} size="small" style={{ marginBottom: 8 }}
                      title={<Form.Item name={[f.name, 'device_id']} noStyle rules={[{ required: true }]}>
                        <Select placeholder="دستگاه" style={{ width: 220 }} options={fgs.map((d) => ({ value: d.id, label: d.name }))} />
                      </Form.Item>}
                      extra={<Button size="small" type="text" danger onClick={() => remove(f.name)}>حذف</Button>}>
                      <ValueFields type={type} path={[f.name, 'value']} />
                    </Card>
                  ))}
                  <Space wrap>
                    <Button onClick={() => add({ value: {} })}>+ نگاشت</Button>
                    {type === 'address' && (
                      <PatternFill onFill={(pattern) => {
                        const cur = (form.getFieldValue('mappings') ?? []) as { device_id?: number }[]
                        const have = new Set(cur.map((m) => m.device_id))
                        const extra = fgs.filter((d) => !have.has(d.id) && deviceNumber(d.name))
                          .map((d) => ({ device_id: d.id, value: { subnet: pattern.replaceAll('{n}', String(Number(deviceNumber(d.name)))) } }))
                        form.setFieldValue('mappings', [...cur, ...extra])
                        message.info(`${faNum(extra.length)} نگاشت از الگو اضافه شد`)
                      }} />
                    )}
                  </Space>
                </div>
              )}
            </Form.List>
          </>
        )}
      </Form>
    </Modal>
  )
}

function PatternFill({ onFill }: { onFill: (p: string) => void }) {
  const [p, setP] = useState('172.16.{n}.0/24')
  return (
    <Space.Compact>
      <Input dir="ltr" value={p} onChange={(e) => setP(e.target.value)} style={{ width: 180 }} />
      <Button onClick={() => p.includes('{n}') && onFill(p)}>پر کردن از الگو</Button>
    </Space.Compact>
  )
}

function ConflictsDrawer({ open, conflicts, onClose, onChanged }: { open: boolean; conflicts: PolicyConflict[]; onClose: () => void; onChanged: () => void }) {
  const { message } = AntApp.useApp()
  const resolve = async (c: PolicyConflict, how: string) => {
    try {
      await api.post(`/policy/conflicts/${c.id}/resolve`, { how })
      message.success('تعارض برطرف شد')
      onChanged()
    } catch (e) {
      message.error((e as Error).message)
    }
  }
  return (
    <Drawer open={open} onClose={onClose} size={720} title={`تعارض‌های Import (${faNum(conflicts.length)})`}>
      <Typography.Paragraph type="secondary">
        این آبجکت‌ها روی دستگاه مقداری متفاوت با پایگاه مشترک دارند. انتخاب کنید مقدار دستگاه نگاشت اختصاصی آن دستگاه شود، مقدار پیش‌فرض شود، یا مقدار مشترک بماند (Install بعدی دستگاه را بازنویسی می‌کند).
      </Typography.Paragraph>
      {conflicts.length === 0 && <Empty description="تعارضی نیست" />}
      {conflicts.map((c) => (
        <Card key={c.id} size="small" style={{ marginBottom: 12 }} title={<Space><Mono>{c.name}</Mono><Tag>{objectTypeLabel[c.type as PolicyObjectType] ?? c.type}</Tag><span>{c.device_name}</span></Space>}>
          <Row gutter={12} style={{ marginBottom: 10 }}>
            <Col span={12}><Typography.Text type="secondary">مقدار مشترک: </Typography.Text><Mono>{valueText(c.type as PolicyObjectType, c.shared)}</Mono></Col>
            <Col span={12}><Typography.Text type="secondary">مقدار دستگاه: </Typography.Text><Mono>{valueText(c.type as PolicyObjectType, c.device_value)}</Mono></Col>
          </Row>
          <Space wrap>
            <Button type="primary" size="small" onClick={() => resolve(c, 'map')}>نگاشت اختصاصی این دستگاه</Button>
            <Button size="small" onClick={() => resolve(c, 'default')}>مقدار دستگاه پیش‌فرض شود</Button>
            <Button size="small" onClick={() => resolve(c, 'keep')}>مقدار مشترک بماند</Button>
          </Space>
        </Card>
      ))}
    </Drawer>
  )
}

// Interfaces maps logical zones to real ports on each FortiGate.
function Interfaces({ devices, canManage }: { devices: DeviceRow[]; canManage: boolean }) {
  const { message } = AntApp.useApp()
  const data = useFetch<{ items: Record<string, Record<string, string>> }>('/policy/interfaces')
  const [edit, setEdit] = useState<DeviceRow | null>(null)
  const [form] = Form.useForm<{ rows: { zone: string; iface: string }[] }>()
  const items = data.data?.items ?? {}
  const zones = [...new Set(Object.values(items).flatMap((m) => Object.keys(m)))].sort()
  const fgs = devices.filter((d) => d.kind === 'fortigate')
  return (
    <>
      <Table rowKey="id" size="middle" dataSource={fgs} pagination={false} loading={data.loading}
        columns={[
          { title: 'دستگاه', dataIndex: 'name' },
          ...zones.map((z) => ({ title: <Mono>{z}</Mono>, key: z, render: (_: unknown, d: DeviceRow) => items[d.id]?.[z] ? <Mono>{items[d.id][z]}</Mono> : <Tag>ندارد</Tag> })),
          ...(canManage ? [{ title: '', key: 'a', width: 90, render: (_: unknown, d: DeviceRow) => <Button size="small" onClick={() => setEdit(d)}>ویرایش</Button> }] : []),
        ]} />
      <Modal open={!!edit} title={`نگاشت اینترفیس ${edit?.name ?? ''}`} onCancel={() => setEdit(null)} okText="ذخیره" destroyOnHidden
        afterOpenChange={(o) => {
          if (!o || !edit) return
          const m = items[edit.id] ?? {}
          const rows = zones.map((zone) => ({ zone, iface: m[zone] ?? '' }))
          form.setFieldsValue({ rows: rows.length ? rows : [{ zone: 'lan', iface: '' }, { zone: 'wan', iface: '' }] })
        }}
        onOk={async () => {
          const v = await form.validateFields()
          const m: Record<string, string> = {}
          for (const r of v.rows ?? []) if (r.zone?.trim() && r.iface?.trim()) m[r.zone.trim()] = r.iface.trim()
          try {
            await api.put(`/policy/interfaces/${edit!.id}`, m)
            message.success('نگاشت ذخیره شد')
            setEdit(null)
            void data.reload()
          } catch (e) {
            message.error((e as Error).message)
          }
        }}>
        <Form form={form}>
          <Form.List name="rows">
            {(fields, { add, remove }) => (
              <>
                {fields.map((f) => (
                  <Space key={f.key} align="baseline">
                    <Form.Item name={[f.name, 'zone']}><Input dir="ltr" placeholder="نام منطقی (lan)" /></Form.Item>
                    <span>→</span>
                    <Form.Item name={[f.name, 'iface']}><Input dir="ltr" placeholder="پورت واقعی (port5)" /></Form.Item>
                    <Button type="text" danger onClick={() => remove(f.name)}>حذف</Button>
                  </Space>
                ))}
                <Button onClick={() => add({ zone: '', iface: '' })}>+ ردیف</Button>
              </>
            )}
          </Form.List>
        </Form>
      </Modal>
    </>
  )
}

export default function PolicyObjects() {
  const { message } = AntApp.useApp()
  const canManage = useCan('policy.manage')
  const data = useFetch<Objects>('/policy/objects')
  const devices = useFetch<{ items: DeviceRow[] }>('/devices')
  const [kind, setKind] = useState<Kind>('addr')
  const [sel, setSel] = useState<number | null>(null)
  const [edit, setEdit] = useState<{ obj: PolicyObject | null } | null>(null)
  const [showConflicts, setShowConflicts] = useState(false)
  const [q, setQ] = useState('')
  const objs = data.data?.items ?? []
  const conflicts = data.data?.conflicts ?? []
  const devs = devices.data?.items ?? []
  const devName = (id: string | number) => devs.find((d) => d.id === Number(id))?.name ?? `#${id}`
  const types = kinds.find((k) => k[0] === kind)![2]
  const rows = objs.filter((o) => types.includes(o.type) && o.name.toLowerCase().includes(q.toLowerCase()))
  const selected = objs.find((o) => o.id === sel) ?? null
  const usage = (name: string) => data.data?.usage[name.toLowerCase()] ?? []
  const names = useMemo(() => ({
    addresses: objs.filter((o) => o.type === 'address' || o.type === 'addrgrp').map((o) => o.name),
    services: objs.filter((o) => o.type === 'service' || o.type === 'servicegrp').map((o) => o.name),
  }), [objs])

  return (
    <>
      <PageHeader title="پایگاه آبجکت مشترک" subtitle="یک تعریف برای همه‌ی دستگاه‌ها، با مقدار اختصاصی برای هر دستگاه در صورت نیاز"
        extra={<Space>
          <Button danger={conflicts.length > 0} onClick={() => setShowConflicts(true)}>تعارض‌های Import ({faNum(conflicts.length)})</Button>
          {canManage && <Button type="primary" onClick={() => setEdit({ obj: null })}>+ آبجکت</Button>}
        </Space>} />
      <Space wrap style={{ marginBottom: 12 }}>
        <Radio.Group value={kind} onChange={(e) => { setKind(e.target.value); setSel(null) }} optionType="button" buttonStyle="solid"
          options={kinds.map(([value, label]) => ({ value, label }))} />
        {types.length > 0 && <Input.Search allowClear placeholder="جستجوی نام" value={q} onChange={(e) => setQ(e.target.value)} style={{ width: 220 }} />}
      </Space>
      {kind === 'prof' && (
        <Card>
          <Alert type="info" showIcon title="پروفایل‌های امنیتی فقط با نام ارجاع داده می‌شوند"
            description="AV، Web Filter، IPS، Application Control و SSL Inspection روی هر دستگاه تعریف می‌شوند و Policyها فقط نامشان را دارند. پیش از Install بررسی می‌شود که پروفایل با همان نام روی دستگاه موجود باشد؛ وگرنه Install آن دستگاه مسدود می‌شود." />
        </Card>
      )}
      {kind === 'iface' && <Card><Interfaces devices={devs} canManage={canManage} /></Card>}
      {types.length > 0 && (
        <Row gutter={16}>
          <Col xs={24} xl={selected ? 15 : 24}>
            <Card styles={{ body: { padding: 0 } }}>
              <Table rowKey="id" size="middle" dataSource={rows} loading={data.loading} pagination={{ pageSize: 20, hideOnSinglePage: true }}
                onRow={(o) => ({ onClick: () => setSel(o.id), style: { cursor: 'pointer', background: o.id === sel ? '#FBF3F6' : undefined } })}
                columns={[
                  { title: 'نام', dataIndex: 'name', render: (n: string) => <Mono>{n}</Mono> },
                  { title: 'نوع', dataIndex: 'type', render: (t: PolicyObjectType) => objectTypeLabel[t] },
                  { title: 'مقدار پیش‌فرض', key: 'v', render: (_, o) => <Mono>{valueText(o.type, o.value)}</Mono> },
                  { title: 'نگاشت اختصاصی', key: 'm', render: (_, o) => {
                    const n = Object.keys(o.mappings ?? {}).length
                    return n ? <Tag color="blue">{faNum(n)} دستگاه</Tag> : o.per_device ? <Tag color="orange">باید نگاشت شود</Tag> : <Tag>ندارد</Tag>
                  } },
                  { title: 'استفاده در', key: 'u', render: (_, o) => {
                    const u = usage(o.name)
                    return u.length ? `${faNum(u.length)} مورد` : <Typography.Text type="secondary">استفاده نشده</Typography.Text>
                  } },
                ]} />
            </Card>
          </Col>
          {selected && (
            <Col xs={24} xl={9}>
              <Card title={<Mono>{selected.name}</Mono>}
                extra={canManage && <Space>
                  <Button size="small" onClick={() => setEdit({ obj: selected })}>ویرایش</Button>
                  <Popconfirm title="این آبجکت حذف شود؟" onConfirm={async () => {
                    try {
                      await api.del(`/policy/objects/${selected.id}`)
                      message.success('حذف شد')
                      setSel(null)
                      void data.reload()
                    } catch (e) {
                      message.error((e as Error).message)
                    }
                  }}><Button size="small" danger>حذف</Button></Popconfirm>
                </Space>}>
                <Space orientation="vertical" style={{ width: '100%' }} size={6}>
                  <div><Typography.Text type="secondary">نوع: </Typography.Text>{objectTypeLabel[selected.type]}</div>
                  <div><Typography.Text type="secondary">پیش‌فرض: </Typography.Text><Mono>{valueText(selected.type, selected.value)}</Mono>{selected.per_device && <Tag color="orange" style={{ marginInlineStart: 6 }}>باید نگاشت شود</Tag>}</div>
                  <div><Typography.Text type="secondary">استفاده: </Typography.Text>{usage(selected.name).join('، ') || '—'}</div>
                  {selected.comment && <div><Typography.Text type="secondary">توضیح: </Typography.Text>{selected.comment}</div>}
                </Space>
                <Typography.Title level={5} style={{ marginTop: 16 }}>نگاشت پویا برای هر دستگاه</Typography.Title>
                <Table size="small" pagination={false} rowKey="id"
                  dataSource={devs.filter((d) => d.kind === 'fortigate' && (selected.mappings?.[d.id] || selected.per_device))}
                  locale={{ emptyText: 'نگاشتی ندارد؛ همه‌ی دستگاه‌ها مقدار پیش‌فرض را می‌گیرند.' }}
                  columns={[
                    { title: 'دستگاه', dataIndex: 'name' },
                    { title: 'مقدار', key: 'v', render: (_, d) => selected.mappings?.[d.id]
                      ? <Mono>{valueText(selected.type, selected.mappings[d.id])}</Mono>
                      : <Typography.Text type="danger">نگاشت ندارد — Install مسدود</Typography.Text> },
                  ]} />
                {Object.keys(selected.mappings ?? {}).filter((d) => !devs.some((x) => x.id === Number(d))).map((d) => <div key={d}>{devName(d)}</div>)}
              </Card>
            </Col>
          )}
        </Row>
      )}
      <ObjectModal open={!!edit} obj={edit?.obj ?? null} defaultType={types[0] ?? 'address'} devices={devs} names={names}
        onClose={() => setEdit(null)} onSaved={() => { setEdit(null); void data.reload() }} />
      <ConflictsDrawer open={showConflicts} conflicts={conflicts} onClose={() => setShowConflicts(false)} onChanged={() => void data.reload()} />
    </>
  )
}

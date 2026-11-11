import { useMemo, useState } from 'react'
import { Alert, AutoComplete, Button, Card, Col, Drawer, Empty, Form, Input, List, Modal, Popconfirm, Row, Select, Space, Switch, Table, Tag, Typography, App as AntApp } from 'antd'
import { Link, useNavigate, useParams } from 'react-router'
import { api, type DeviceRow, type FwPolicy, type PolicyObject, type PolicyPackage, type PolicyStatus, type PolicyVersion } from '../api'
import { useAuth, useCan } from '../auth'
import { faDateTime, faNum } from '../format'
import { ObjChips, opLabel, policyStateTag, profileText } from '../policyui'
import { Mono, PageHeader } from '../ui'
import { useFetch } from '../useFetch'

type PkgRow = PolicyPackage & { statuses: PolicyStatus[] | null }
type PkgDetail = { package: PolicyPackage; published: FwPolicy[] | null; statuses: PolicyStatus[] | null; versions: PolicyVersion[] | null; hits: Record<string, number> }

const builtinServices = ['ALL', 'ALL_TCP', 'ALL_UDP', 'ALL_ICMP', 'HTTP', 'HTTPS', 'DNS', 'SSH', 'PING', 'SMTP', 'RDP', 'NTP', 'SNMP', 'FTP', 'Web Access', 'Email Access', 'Windows AD']
const opts = (xs: string[]) => [...new Set(xs)].map((x) => ({ value: x, label: x }))

const hitText = (n: number | undefined) => {
  if (n === undefined) return '—'
  if (n >= 1e6) return `${faNum(Math.round(n / 1e5) / 10)} M`
  if (n >= 1e3) return `${faNum(Math.round(n / 1e2) / 10)} K`
  return faNum(n)
}

// PolicyModal edits one firewall policy of the draft.
function PolicyModal({ open, policy, objects, zones, sections, onClose, onSave }: {
  open: boolean; policy: FwPolicy | null; objects: PolicyObject[]; zones: string[]; sections: string[]
  onClose: () => void; onSave: (p: FwPolicy) => Promise<void>
}) {
  const [form] = Form.useForm<FwPolicy>()
  const nat = Form.useWatch('nat', form)
  const names = (...t: string[]) => objects.filter((o) => t.includes(o.type)).map((o) => o.name)
  const ltr = { direction: 'ltr' as const }
  return (
    <Modal open={open} width={760} title={policy?.name ? `Policy ${policy.id} · ${policy.name}` : 'Policy جدید'} okText="ثبت در پیش‌نویس" destroyOnHidden
      onCancel={onClose} onOk={() => form.submit()}
      afterOpenChange={(o) => {
        if (!o) return
        form.resetFields()
        form.setFieldsValue(policy ?? ({ action: 'accept', schedule: 'always', logtraffic: 'utm', srcaddr: [], dstaddr: ['all'], service: ['ALL'], srcintf: [], dstintf: [], nat: false, disabled: false } as Partial<FwPolicy> as FwPolicy))
      }}>
      <Form form={form} layout="vertical" onFinish={(v) => onSave({ ...policy, ...v, id: Number(v.id) })}>
        <Row gutter={12}>
          <Col span={5}><Form.Item name="id" label="شناسه" rules={[{ required: true }]}><Input style={ltr} type="number" min={1} /></Form.Item></Col>
          <Col span={11}><Form.Item name="name" label="نام" rules={[{ required: true, message: 'نام را وارد کنید' }]}><Input style={ltr} maxLength={35} /></Form.Item></Col>
          <Col span={8}><Form.Item name="section" label="بخش"><AutoComplete options={opts(sections)} placeholder="مثلاً دسترسی کاربران" filterOption /></Form.Item></Col>
        </Row>
        <Row gutter={12}>
          <Col span={12}><Form.Item name="srcintf" label="از (اینترفیس منطقی)" rules={[{ required: true, message: 'اینترفیس مبدأ' }]}><Select mode="tags" style={ltr} options={opts(['any', ...zones])} /></Form.Item></Col>
          <Col span={12}><Form.Item name="dstintf" label="به (اینترفیس منطقی)" rules={[{ required: true, message: 'اینترفیس مقصد' }]}><Select mode="tags" style={ltr} options={opts(['any', ...zones])} /></Form.Item></Col>
          <Col span={12}><Form.Item name="srcaddr" label="مبدأ" rules={[{ required: true, message: 'مبدأ' }]}><Select mode="tags" style={ltr} options={opts(['all', ...names('address', 'addrgrp')])} /></Form.Item></Col>
          <Col span={12}><Form.Item name="dstaddr" label="مقصد" rules={[{ required: true, message: 'مقصد' }]}><Select mode="tags" style={ltr} options={opts(['all', ...names('address', 'addrgrp', 'vip')])} /></Form.Item></Col>
          <Col span={12}><Form.Item name="service" label="سرویس" rules={[{ required: true, message: 'سرویس' }]}><Select mode="tags" style={ltr} options={opts([...builtinServices, ...names('service', 'servicegrp')])} /></Form.Item></Col>
          <Col span={12}><Form.Item name="schedule" label="زمان‌بندی"><Select style={ltr} options={opts(['always', ...names('schedule')])} /></Form.Item></Col>
          <Col span={8}><Form.Item name="action" label="اقدام"><Select options={[{ value: 'accept', label: 'اجازه' }, { value: 'deny', label: 'رد' }]} /></Form.Item></Col>
          <Col span={8}><Form.Item name="logtraffic" label="ثبت لاگ"><Select options={[{ value: 'all', label: 'همه' }, { value: 'utm', label: 'رخدادهای امنیتی' }, { value: 'disable', label: 'خاموش' }]} /></Form.Item></Col>
          <Col span={4}><Form.Item name="nat" label="NAT" valuePropName="checked"><Switch /></Form.Item></Col>
          <Col span={4}><Form.Item name="disabled" label="غیرفعال" valuePropName="checked"><Switch /></Form.Item></Col>
          {nat && <Col span={24}><Form.Item name="ippool" label="IP Pool (خالی = آدرس اینترفیس خروجی)"><Select mode="multiple" style={ltr} options={opts(names('ippool'))} /></Form.Item></Col>}
        </Row>
        <Typography.Text strong>پروفایل‌های امنیتی (با نام؛ باید روی دستگاه موجود باشند)</Typography.Text>
        <Row gutter={12} style={{ marginTop: 8 }}>
          <Col span={8}><Form.Item name="av_profile" label="Antivirus"><Input style={ltr} placeholder="default" /></Form.Item></Col>
          <Col span={8}><Form.Item name="webfilter_profile" label="Web Filter"><Input style={ltr} /></Form.Item></Col>
          <Col span={8}><Form.Item name="ips_sensor" label="IPS"><Input style={ltr} /></Form.Item></Col>
          <Col span={8}><Form.Item name="application_list" label="Application Control"><Input style={ltr} /></Form.Item></Col>
          <Col span={8}><Form.Item name="ssl_ssh_profile" label="SSL Inspection"><Input style={ltr} placeholder="certificate-inspection" /></Form.Item></Col>
          <Col span={8}><Form.Item name="comments" label="توضیح"><Input /></Form.Item></Col>
        </Row>
      </Form>
    </Modal>
  )
}

function PackageModal({ open, pkg, devices, onClose, onSaved }: { open: boolean; pkg: PolicyPackage | null; devices: DeviceRow[]; onClose: () => void; onSaved: (id: number) => void }) {
  const { message } = AntApp.useApp()
  const [form] = Form.useForm()
  return (
    <Modal open={open} title={pkg ? `تنظیمات ${pkg.name}` : 'Policy Package جدید'} okText="ذخیره" destroyOnHidden onCancel={onClose} onOk={() => form.submit()}
      afterOpenChange={(o) => { if (o) { form.resetFields(); form.setFieldsValue(pkg ?? { device_ids: [], vdom: '' }) } }}>
      <Form form={form} layout="vertical" onFinish={async (v) => {
        try {
          const body = { name: v.name.trim(), description: v.description ?? '', device_ids: v.device_ids ?? [], vdom: v.vdom ?? '' }
          const r = pkg ? await api.put<{ id: number }>(`/policy/packages/${pkg.id}`, body) : await api.post<{ id: number }>('/policy/packages', body)
          message.success('ذخیره شد')
          onSaved(r.id)
        } catch (e) {
          message.error((e as Error).message)
        }
      }}>
        <Form.Item name="name" label="نام" rules={[{ required: true, message: 'نام را وارد کنید' }]}><Input /></Form.Item>
        <Form.Item name="description" label="توضیح"><Input /></Form.Item>
        <Form.Item name="device_ids" label="دستگاه‌ها" extra="هر دستگاه فقط در یک Package قرار می‌گیرد.">
          <Select mode="multiple" optionFilterProp="label" options={devices.filter((d) => d.kind === 'fortigate').map((d) => ({ value: d.id, label: d.name }))} />
        </Form.Item>
        <Form.Item name="vdom" label="VDOM" extra="برای دستگاه‌های بدون VDOM خالی بگذارید."><Input dir="ltr" style={{ width: 200 }} /></Form.Item>
      </Form>
    </Modal>
  )
}

export default function PolicyPackages() {
  const { id } = useParams()
  const nav = useNavigate()
  const { message, modal } = AntApp.useApp()
  const { user } = useAuth()
  const canManage = useCan('policy.manage')
  const canInstall = useCan('changes.create')
  const list = useFetch<{ items: PkgRow[] }>('/policy/packages')
  const pkgID = id ? Number(id) : list.data?.items[0]?.id
  const detail = useFetch<PkgDetail>(pkgID ? `/policy/packages/${pkgID}` : null)
  const objects = useFetch<{ items: PolicyObject[] }>('/policy/objects')
  const ifaces = useFetch<{ items: Record<string, Record<string, string>> }>('/policy/interfaces')
  const devices = useFetch<{ items: DeviceRow[] }>('/devices')
  const [editPolicy, setEditPolicy] = useState<{ p: FwPolicy | null } | null>(null)
  const [pkgModal, setPkgModal] = useState<{ pkg: PolicyPackage | null } | null>(null)
  const [showVersions, setShowVersions] = useState(false)
  const [importOpen, setImportOpen] = useState(false)
  const [importDev, setImportDev] = useState<number | null>(null)
  const [statusDev, setStatusDev] = useState<PolicyStatus | null>(null)
  const [busy, setBusy] = useState(false)

  const d = detail.data
  const pkg = d?.package
  const me = user?.username ?? ''
  const lockedByOther = !!pkg?.locked_by && pkg.locked_by !== me
  const editing = !!pkg && pkg.locked_by === me
  const working: FwPolicy[] = (pkg?.has_draft ? pkg.draft : d?.published) ?? []
  const published = useMemo(() => new Map((d?.published ?? []).map((p) => [p.id, JSON.stringify(p)])), [d?.published])
  const changed = (p: FwPolicy) => pkg?.has_draft && published.get(p.id) !== JSON.stringify(p)
  const removed = pkg?.has_draft ? (d?.published ?? []).filter((p) => !working.some((w) => w.id === p.id)) : []
  const zones = [...new Set(Object.values(ifaces.data?.items ?? {}).flatMap((m) => Object.keys(m)))]
  const sections = [...new Set(working.map((p) => p.section ?? '').filter(Boolean))]
  const reload = () => { void detail.reload(); void list.reload() }

  const run = async (fn: () => Promise<unknown>, ok?: string) => {
    setBusy(true)
    try {
      await fn()
      if (ok) message.success(ok)
      reload()
      return true
    } catch (e) {
      message.error((e as Error).message)
      return false
    } finally {
      setBusy(false)
    }
  }
  const saveDraft = (policies: FwPolicy[]) => run(() => api.post(`/policy/packages/${pkgID}/draft`, { policies }))
  const move = (i: number, dir: -1 | 1) => {
    const next = [...working]
    const j = i + dir
    if (j < 0 || j >= next.length) return
    ;[next[i], next[j]] = [next[j], next[i]]
    void saveDraft(next)
  }

  // Group consecutive policies by section, keeping the install order.
  const groups: { title: string; rows: { p: FwPolicy; i: number }[] }[] = []
  working.forEach((p, i) => {
    const t = p.section || 'بدون بخش'
    if (!groups.length || groups[groups.length - 1].title !== t) groups.push({ title: t, rows: [] })
    groups[groups.length - 1].rows.push({ p, i })
  })

  const statuses = d?.statuses ?? []
  const pkgDevices = (devices.data?.items ?? []).filter((x) => pkg?.device_ids.includes(x.id))

  return (
    <>
      <PageHeader title={pkg ? pkg.name : 'Policy Package'}
        subtitle={pkg && (
          <Space wrap>
            <span>{pkg.has_draft ? `پیش‌نویس v${faNum(pkg.version + 1)} · بر پایه‌ی v${faNum(pkg.draft_base ?? pkg.version)}` : pkg.version ? `نسخه‌ی منتشرشده v${faNum(pkg.version)}` : 'هنوز منتشر نشده'}</span>
            {pkg.locked_by && <Tag color={editing ? 'blue' : 'orange'}>قفل ویرایش: {editing ? 'شما' : pkg.locked_by}</Tag>}
          </Space>
        )}
        extra={pkg && (
          <Space wrap>
            <Button onClick={() => setShowVersions(true)}>تاریخچه‌ی نسخه‌ها</Button>
            {canManage && <Button onClick={() => { setImportDev(pkg.device_ids[0] ?? null); setImportOpen(true) }} disabled={lockedByOther}>Import از دستگاه</Button>}
            {canManage && !editing && <Button disabled={lockedByOther} loading={busy} onClick={() => run(() => api.post(`/policy/packages/${pkg.id}/lock`), 'ویرایش شروع شد')}>شروع ویرایش</Button>}
            {editing && <Button onClick={() => setEditPolicy({ p: null })}>+ Policy</Button>}
            {editing && pkg.has_draft && (
              <Button type="primary" loading={busy} onClick={() => {
                let note = ''
                modal.confirm({
                  title: `انتشار نسخه‌ی v${faNum(pkg.version + 1)}`, okText: 'انتشار',
                  content: <Input placeholder="یادداشت تغییرات" onChange={(e) => { note = e.target.value }} />,
                  onOk: () => run(() => api.post(`/policy/packages/${pkg.id}/publish`, { note }), 'نسخه منتشر شد'),
                })
              }}>انتشار</Button>
            )}
            {editing && pkg.has_draft && (
              <Popconfirm title="تغییرات پیش‌نویس کنار گذاشته شود؟" onConfirm={() => run(() => api.post(`/policy/packages/${pkg.id}/discard`), 'پیش‌نویس کنار گذاشته شد')}>
                <Button danger>کنار گذاشتن پیش‌نویس</Button>
              </Popconfirm>
            )}
            {editing && <Button onClick={() => run(() => api.post(`/policy/packages/${pkg.id}/unlock`), 'قفل آزاد شد')}>پایان ویرایش</Button>}
            {canInstall && pkg.version > 0 && <Button type="primary" onClick={() => nav(`/policy/${pkg.id}/install`)}>Install…</Button>}
          </Space>
        )} />
      {lockedByOther && <Alert type="warning" showIcon style={{ marginBottom: 12 }} title={`«${pkg!.locked_by}» در حال ویرایش این Package است؛ تا پایان ویرایش او فقط مشاهده ممکن است.`} />}
      {editing && pkg?.has_draft && pkg.draft_base !== null && pkg.draft_base !== pkg.version && (
        <Alert type="error" showIcon style={{ marginBottom: 12 }} title="پس از شروع این پیش‌نویس نسخه‌ی جدیدی منتشر شده است؛ پیش از انتشار، پیش‌نویس را کنار بگذارید یا از نسخه‌ی تازه دوباره شروع کنید." />
      )}
      <Row gutter={16}>
        <Col xs={24} lg={6}>
          <Card size="small" title="Policy Packageها" extra={canManage && <Button size="small" type="link" onClick={() => setPkgModal({ pkg: null })}>+ جدید</Button>} style={{ marginBottom: 12 }}>
            {(list.data?.items ?? []).length === 0 && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="هنوز Packageی نیست" />}
            {(list.data?.items ?? []).map((p) => (
              <Link key={p.id} to={`/policy/${p.id}`} style={{ display: 'flex', justifyContent: 'space-between', padding: '8px 10px', borderRadius: 8, color: 'inherit',
                background: p.id === pkgID ? '#FBF3F6' : undefined, fontWeight: p.id === pkgID ? 600 : 400 }}>
                <span>{p.name}</span>
                <Space size={4}>
                  {(p.statuses ?? []).some((s) => s.state === 'local_changes') && <Tag color="red" style={{ marginInlineEnd: 0 }}>تغییر محلی</Tag>}
                  <Typography.Text type="secondary">{faNum(p.device_ids.length)} دستگاه</Typography.Text>
                </Space>
              </Link>
            ))}
          </Card>
          {pkg && (
            <Card size="small" title={`دستگاه‌های ${pkg.name}`} extra={canManage && <Button size="small" type="link" onClick={() => setPkgModal({ pkg })}>تنظیمات</Button>}>
              {statuses.length === 0 && <Typography.Text type="secondary">دستگاهی انتخاب نشده است.</Typography.Text>}
              {statuses.map((s) => (
                <div key={s.device_id} onClick={() => setStatusDev(s)} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '6px 4px', cursor: 'pointer' }}>
                  <span>{s.device_name}</span>
                  <Space size={4}>{s.installed > 0 && <Typography.Text type="secondary" style={{ fontSize: 12 }}>v{faNum(s.installed)}</Typography.Text>}{policyStateTag(s.state)}</Space>
                </div>
              ))}
            </Card>
          )}
        </Col>
        <Col xs={24} lg={18}>
          {!pkg && !list.loading && (
            <Card><Empty description="برای شروع یک Policy Package بسازید و سپس Policyها را از یک دستگاه Import کنید یا دستی اضافه کنید.">
              {canManage && <Button type="primary" onClick={() => setPkgModal({ pkg: null })}>Policy Package جدید</Button>}
            </Empty></Card>
          )}
          {pkg && (
            <Card styles={{ body: { padding: 0 } }} loading={detail.loading && !d}>
              <Table<{ p: FwPolicy; i: number } | { section: string }> size="small" pagination={false} scroll={{ x: 1100 }}
                rowKey={(r) => ('section' in r ? `s:${r.section}` : `p:${r.p.id}`)}
                dataSource={groups.flatMap((g) => [{ section: g.title }, ...g.rows])}
                locale={{ emptyText: 'Policyی ندارد؛ از دستگاه Import کنید یا «+ Policy» را بزنید.' }}
                onRow={(r) => ('section' in r ? { style: { background: '#F8FAFC' } } : { style: { background: changed(r.p) ? '#FFFBF2' : undefined, opacity: r.p.disabled ? 0.55 : 1 } })}
                columns={[
                  { title: '#', key: 'id', width: 60, onCell: (r) => ('section' in r ? { colSpan: 11 } : {}),
                    render: (_, r) => ('section' in r ? <Typography.Text strong>{r.section}</Typography.Text> : <Mono>{r.p.id}</Mono>) },
                  ...([
                    { title: 'نام', key: 'name', render: (p: FwPolicy) => <Space size={4} style={{ whiteSpace: 'nowrap' }}><Mono>{p.name}</Mono>{changed(p) && <Tag color="orange">{published.has(p.id) ? 'تغییر' : 'جدید'}</Tag>}{p.disabled && <Tag>غیرفعال</Tag>}</Space> },
                    { title: 'از', key: 'from', render: (p: FwPolicy) => <ObjChips names={p.srcintf} /> },
                    { title: 'به', key: 'to', render: (p: FwPolicy) => <ObjChips names={p.dstintf} /> },
                    { title: 'مبدأ', key: 'src', render: (p: FwPolicy) => <ObjChips names={p.srcaddr} /> },
                    { title: 'مقصد', key: 'dst', render: (p: FwPolicy) => <ObjChips names={p.dstaddr} /> },
                    { title: 'سرویس', key: 'svc', render: (p: FwPolicy) => <ObjChips names={p.service} /> },
                    { title: 'اقدام', key: 'act', render: (p: FwPolicy) => (p.action === 'accept' ? <Tag color="green">اجازه</Tag> : <Tag color="red">رد</Tag>) },
                    { title: 'پروفایل‌های امنیتی', key: 'utm', render: (p: FwPolicy) => profileText(p) || '—' },
                    { title: 'NAT', key: 'nat', render: (p: FwPolicy) => (p.nat ? (p.ippool?.length ? `Pool ${p.ippool.join('، ')}` : 'SNAT') : '—') },
                    { title: 'Hit (۳۰ روز)', key: 'hits', render: (p: FwPolicy) => hitText(d?.hits[String(p.id)]) },
                  ] as const).map((c) => ({ ...c, onCell: (r: { p: FwPolicy } | { section: string }) => ('section' in r ? { colSpan: 0 } : {}), render: (_: unknown, r: { p: FwPolicy; i: number } | { section: string }) => ('section' in r ? null : c.render(r.p)) })),
                  ...(editing ? [{
                    title: '', key: 'ops', width: 170, fixed: 'left' as const,
                    onCell: (r: { p: FwPolicy } | { section: string }) => ('section' in r ? { colSpan: 0 } : {}),
                    render: (_: unknown, r: { p: FwPolicy; i: number } | { section: string }) => ('section' in r ? null : (
                      <Space size={2}>
                        <Button size="small" type="text" onClick={() => move(r.i, -1)} disabled={r.i === 0}>▲</Button>
                        <Button size="small" type="text" onClick={() => move(r.i, 1)} disabled={r.i === working.length - 1}>▼</Button>
                        <Button size="small" type="link" onClick={() => setEditPolicy({ p: r.p })}>ویرایش</Button>
                        <Popconfirm title="این Policy از پیش‌نویس حذف شود؟" onConfirm={() => saveDraft(working.filter((x) => x.id !== r.p.id))}>
                          <Button size="small" type="link" danger>حذف</Button>
                        </Popconfirm>
                      </Space>
                    )),
                  }] : []),
                ]} />
              {removed.length > 0 && (
                <div style={{ padding: 12 }}>
                  <Typography.Text type="danger">حذف‌شده در پیش‌نویس: </Typography.Text>
                  {removed.map((p) => <Tag key={p.id} color="red">{p.id} · {p.name}</Tag>)}
                </div>
              )}
              <Typography.Paragraph type="secondary" style={{ padding: '10px 12px', margin: 0, fontSize: 12 }}>
                نام اینترفیس‌ها منطقی هستند (<Mono>lan</Mono>، <Mono>wan</Mono>، <Mono>dmz</Mono>) و برای هر دستگاه با «نگاشت اینترفیس» به پورت واقعی تبدیل می‌شوند. آبجکت‌ها از <Link to="/policy/objects">پایگاه آبجکت مشترک</Link> می‌آیند. ترتیب جدول همان ترتیب اجرای Policyها روی دستگاه است.
              </Typography.Paragraph>
            </Card>
          )}
        </Col>
      </Row>

      <PolicyModal open={!!editPolicy} policy={editPolicy?.p ?? null} objects={objects.data?.items ?? []} zones={zones} sections={sections}
        onClose={() => setEditPolicy(null)}
        onSave={async (p) => {
          const orig = editPolicy?.p
          if (working.some((x) => x.id === p.id && x !== orig)) {
            message.error(`Policy با شناسه‌ی ${p.id} وجود دارد`)
            return
          }
          const next = orig ? working.map((x) => (x === orig ? p : x)) : [...working, p]
          if (await saveDraft(next)) setEditPolicy(null)
        }} />
      <PackageModal open={!!pkgModal} pkg={pkgModal?.pkg ?? null} devices={devices.data?.items ?? []}
        onClose={() => setPkgModal(null)} onSaved={(nid) => { setPkgModal(null); reload(); nav(`/policy/${nid}`) }} />

      <Drawer open={showVersions} onClose={() => setShowVersions(false)} size={560} title="تاریخچه‌ی نسخه‌ها">
        <List dataSource={d?.versions ?? []} locale={{ emptyText: 'هنوز نسخه‌ای منتشر نشده است' }}
          renderItem={(v) => (
            <List.Item actions={canManage && v.version !== pkg?.version ? [
              <Popconfirm key="r" title={`v${v.version} به‌عنوان پیش‌نویس بارگذاری شود؟`} onConfirm={async () => {
                if (!editing && !(await run(() => api.post(`/policy/packages/${pkgID}/lock`)))) return
                if (await run(() => api.post(`/policy/packages/${pkgID}/revert`, { version: v.version }), 'نسخه در پیش‌نویس بارگذاری شد؛ برای اعمال منتشر کنید')) setShowVersions(false)
              }}><Button size="small">بازگشت به این نسخه</Button></Popconfirm>,
            ] : []}>
              <List.Item.Meta title={<Space>v{faNum(v.version)}{v.version === pkg?.version && <Tag color="green">فعلی</Tag>}</Space>}
                description={<>{faDateTime(v.created_at)} · {v.created_by} · {faNum(v.policies?.length ?? 0)} Policy{v.note && <div>{v.note}</div>}</>} />
            </List.Item>
          )} />
      </Drawer>

      <Modal open={importOpen} title="Import از دستگاه" okText="Import" confirmLoading={busy} onCancel={() => setImportOpen(false)}
        onOk={async () => {
          if (!importDev) return
          setBusy(true)
          try {
            const r = await api.post<{ policies: number; created: string[]; conflicts: string[]; zones: string[] }>(`/policy/packages/${pkgID}/import`, { device_id: importDev })
            setImportOpen(false)
            reload()
            void objects.reload()
            void ifaces.reload()
            modal.info({
              title: 'Import انجام شد',
              content: (
                <div>
                  <div>{faNum(r.policies)} Policy خوانده شد{pkg?.version || pkg?.has_draft ? '' : ' و در پیش‌نویس قرار گرفت'}.</div>
                  <div>آبجکت‌های جدید: {faNum(r.created.length)}</div>
                  <div>تعارض‌ها: {faNum(r.conflicts.length)}{r.conflicts.length > 0 && <> — <Link to="/policy/objects">بررسی در آبجکت‌ها</Link></>}</div>
                  {r.zones.length > 0 && <div>اینترفیس‌های نگاشت‌شده: <Mono>{r.zones.join(', ')}</Mono></div>}
                </div>
              ),
            })
          } catch (e) {
            message.error((e as Error).message)
          } finally {
            setBusy(false)
          }
        }}>
        <Typography.Paragraph type="secondary">
          یک بکاپ تازه از دستگاه گرفته و آبجکت‌ها، نگاشت اینترفیس‌ها و Policyهای آن خوانده می‌شود. آبجکت‌هایی که با پایگاه مشترک تفاوت دارند به‌صورت تعارض ثبت می‌شوند. Policyها فقط وقتی Package خالی باشد وارد پیش‌نویس می‌شوند.
        </Typography.Paragraph>
        <Select style={{ width: '100%' }} value={importDev} onChange={setImportDev} placeholder="دستگاه"
          options={(pkgDevices.length ? pkgDevices : (devices.data?.items ?? []).filter((x) => x.kind === 'fortigate')).map((x) => ({ value: x.id, label: x.name }))} />
      </Modal>

      <Drawer open={!!statusDev} onClose={() => setStatusDev(null)} size={620} title={statusDev ? <Space>{statusDev.device_name}{policyStateTag(statusDev.state)}</Space> : ''}>
        {statusDev && (
          <>
            <Typography.Paragraph type="secondary">
              نسخه‌ی نصب‌شده: {statusDev.installed ? `v${faNum(statusDev.installed)} · ${faDateTime(statusDev.installed_at)}` : 'نصب نشده'}
              {statusDev.checked_at && <> · آخرین بررسی: {faDateTime(statusDev.checked_at)}</>}
            </Typography.Paragraph>
            {statusDev.state === 'local_changes' && (
              <>
                <Alert type="warning" showIcon style={{ marginBottom: 12 }} title="این تغییرات مستقیماً روی دستگاه انجام شده و در Package نیست"
                  description="Install بعدی آن‌ها را بازنویسی می‌کند. برای نگه داشتن، «پذیرفتن تغییر محلی» Policyهای دستگاه را وارد پیش‌نویس و مقادیر متفاوت آبجکت‌ها را نگاشت اختصاصی این دستگاه می‌کند." />
                <Table size="small" rowKey={(_, i) => String(i)} pagination={false} dataSource={statusDev.drift ?? []}
                  columns={[
                    { title: 'برای هم‌گام‌سازی', dataIndex: 'action', render: (a: string) => <Tag color={opLabel[a]?.[1]}>{opLabel[a]?.[0] ?? a}</Tag> },
                    { title: 'نوع', dataIndex: 'type' },
                    { title: 'شیء', dataIndex: 'name', render: (n: string) => <Mono>{n}</Mono> },
                    { title: 'جزئیات', dataIndex: 'detail' },
                  ]} />
                {canManage && (
                  <Button type="primary" style={{ marginTop: 12 }} disabled={lockedByOther} loading={busy}
                    onClick={async () => {
                      if (await run(() => api.post(`/policy/packages/${pkgID}/accept`, { device_id: statusDev.device_id }), 'تغییرات محلی وارد پیش‌نویس شد؛ برای اعمال منتشر کنید')) setStatusDev(null)
                    }}>پذیرفتن تغییر محلی</Button>
                )}
              </>
            )}
            {statusDev.state === 'outdated' && <Alert type="info" showIcon title={`نسخه‌ی v${faNum(pkg?.version ?? 0)} هنوز روی این دستگاه نصب نشده است.`} />}
            {statusDev.state === 'in_sync' && <Alert type="success" showIcon title="کانفیگ دستگاه با نسخه‌ی نصب‌شده هم‌گام است." />}
          </>
        )}
      </Drawer>
    </>
  )
}

import { useState } from 'react'
import { Alert, Button, Card, Form, Input, Modal, Popconfirm, Progress, Radio, Space, Switch, Table, Tabs, Tag, Typography, App as AntApp } from 'antd'
import { api, type DirectoryUser, type GatewayUser, type OtpOverview, type OtpUser } from '../api'
import { useCan } from '../auth'
import { faDateTime, faNum } from '../format'
import { Mono, PageHeader } from '../ui'
import { useFetch } from '../useFetch'

function ImportModal({ open, onClose }: { open: boolean; onClose: (n: number) => void }) {
  const [list, setList] = useState<DirectoryUser[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [selected, setSelected] = useState<string[]>([])
  const [group, setGroup] = useState('')
  const [busy, setBusy] = useState(false)
  const load = async () => {
    setError(null)
    setList(null)
    try {
      const r = await api.get<{ items: DirectoryUser[] }>('/otp/directory')
      setList(r.items)
    } catch (e) {
      setError((e as Error).message)
    }
  }
  return (
    <Modal open={open} title="وارد کردن کاربران از LDAP/AD" width={720} okText="وارد کردن" cancelText="انصراف" confirmLoading={busy}
      okButtonProps={{ disabled: !selected.length }} afterOpenChange={(o) => { if (o) { setSelected([]); void load() } }} onCancel={() => onClose(0)}
      onOk={async () => {
        setBusy(true)
        try {
          const r = await api.post<{ imported: number }>('/otp/directory/import', { usernames: selected, fortinet_group: group })
          onClose(r.imported)
        } catch (e) {
          setError((e as Error).message)
        } finally {
          setBusy(false)
        }
      }}>
      {error && <Alert type="error" showIcon title={error} style={{ marginBottom: 12 }} />}
      <Input placeholder="گروه Fortinet برای کاربران واردشده (اختیاری)" value={group} onChange={(e) => setGroup(e.target.value)} style={{ marginBottom: 12 }} />
      <Table<DirectoryUser> rowKey="username" size="small" loading={list === null && !error} dataSource={list ?? []} pagination={{ pageSize: 10, hideOnSinglePage: true }}
        rowSelection={{ selectedRowKeys: selected, onChange: (k) => setSelected(k as string[]), getCheckboxProps: (u) => ({ disabled: u.exists || !u.valid }) }}
        columns={[
          { title: 'نام کاربری', render: (_, u) => <Mono>{u.username}</Mono> },
          { title: 'نام', dataIndex: 'name' },
          { title: 'موبایل', render: (_, u) => (u.valid ? <Mono>{u.mobile}</Mono> : <Typography.Text type="danger">{u.mobile || 'ندارد'}</Typography.Text>) },
          { title: '', render: (_, u) => (u.exists ? <Tag>موجود</Tag> : null) },
        ]} />
    </Modal>
  )
}

export default function OtpUsers() {
  const { message } = AntApp.useApp()
  const canManage = useCan('otp.manage')
  const overview = useFetch<OtpOverview>('/otp')
  const users = useFetch<{ items: OtpUser[] }>('/otp/users')
  const gw = useFetch<{ items: GatewayUser[] }>('/otp/gateway/users')
  const [editing, setEditing] = useState<Partial<OtpUser> | null>(null)
  const [importing, setImporting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [form] = Form.useForm()
  const source = Form.useWatch('source', form)
  const o = overview.data
  const reload = () => { void users.reload(); void overview.reload(); void gw.reload() }

  return (
    <>
      <PageHeader title="کاربران دو مرحله‌ای" subtitle="کاربرانی که با رمز و کد پیامکی به FortiGate وارد می‌شوند"
        extra={canManage && (
          <>
            {o?.ldap.url && <Button onClick={() => setImporting(true)}>ورود از LDAP/AD</Button>}
            <Button type="primary" onClick={() => { setError(null); form.resetFields(); form.setFieldsValue({ source: 'local', active: true }); setEditing({}) }}>+ کاربر</Button>
          </>
        )} />
      {o && (
        <Card style={{ marginBottom: 16 }}>
          <Space size={32} wrap>
            <div style={{ minWidth: 260 }}>
              <Typography.Text type="secondary">ظرفیت لایسنس</Typography.Text>
              <Progress percent={o.capacity ? Math.round((o.used / o.capacity) * 100) : 100} status={o.used > o.capacity ? 'exception' : undefined}
                format={() => `${faNum(o.used)} از ${faNum(o.capacity)}`} />
            </div>
            <div><Typography.Text type="secondary">پیامک امروز</Typography.Text><div style={{ fontSize: 20 }}>{faNum(o.stats.sent_day)} <Typography.Text type="danger" style={{ fontSize: 13 }}>{o.stats.failed_day ? `${faNum(o.stats.failed_day)} ناموفق` : ''}</Typography.Text></div></div>
            <div><Typography.Text type="secondary">پیامک ۳۰ روز</Typography.Text><div style={{ fontSize: 20 }}>{faNum(o.stats.sent_month)}</div></div>
          </Space>
          {!o.sms_configured && <Alert type="warning" showIcon style={{ marginTop: 12 }} title="ارسال پیامک تنظیم نشده است؛ کلید و قالب sms.ir را در تنظیمات دو مرحله‌ای وارد کنید." />}
        </Card>
      )}
      <Tabs items={[
        {
          key: 'users', label: 'کاربران RADIUS', children: (
            <Card>
              <Table<OtpUser> rowKey="id" loading={users.loading} dataSource={users.data?.items ?? []} scroll={{ x: 900 }} pagination={{ pageSize: 30, hideOnSinglePage: true }}
                locale={{ emptyText: 'کاربری تعریف نشده' }}
                columns={[
                  { title: 'نام کاربری', render: (_, u) => <Mono>{u.username}</Mono> },
                  { title: 'نام', dataIndex: 'name' },
                  { title: 'موبایل', render: (_, u) => <Mono>{u.mobile}</Mono> },
                  { title: 'رمز', render: (_, u) => (u.source === 'ldap' ? <Tag color="blue">LDAP/AD</Tag> : <Tag>محلی</Tag>) },
                  { title: 'گروه Fortinet', render: (_, u) => (u.fortinet_group ? <Mono>{u.fortinet_group}</Mono> : '—') },
                  { title: 'وضعیت', render: (_, u) => <Space size={4}>{u.active ? <Tag color="green">فعال</Tag> : <Tag>غیرفعال</Tag>}{u.locked && <Tag color="red">قفل</Tag>}</Space> },
                  { title: 'آخرین ورود', render: (_, u) => faDateTime(u.last_login_at) },
                  ...(canManage ? [{
                    title: '', render: (_: unknown, u: OtpUser) => (
                      <Space size={0}>
                        <Button size="small" type="link" onClick={() => { setError(null); form.resetFields(); form.setFieldsValue(u); setEditing(u) }}>ویرایش</Button>
                        {u.locked && <Button size="small" type="link" onClick={async () => { await api.post(`/otp/users/${u.id}/unlock`); reload() }}>باز کردن قفل</Button>}
                        <Popconfirm title="کاربر حذف شود؟" okText="حذف" cancelText="انصراف" onConfirm={async () => { await api.del(`/otp/users/${u.id}`).catch((e) => message.error(e.message)); reload() }}>
                          <Button size="small" type="link" danger>حذف</Button>
                        </Popconfirm>
                      </Space>
                    ),
                  }] : []),
                ]} />
            </Card>
          ),
        },
        {
          key: 'gateway', label: 'کاربران Local فورتی‌گیت', children: (
            <Card extra={canManage && <Button onClick={async () => {
              try { await api.post('/otp/gateway/sync'); message.success('همگام شد') } catch (e) { message.error((e as Error).message) }
              reload()
            }}>همگام‌سازی از دستگاه‌ها</Button>}>
              <Typography.Paragraph type="secondary">کاربران Local با two-factor sms روی فورتی‌گیت‌ها؛ هر ساعت خوانده می‌شوند و در ظرفیت لایسنس شمرده می‌شوند.</Typography.Paragraph>
              <Table<GatewayUser> rowKey={(u) => `${u.device_id}-${u.username}`} size="small" dataSource={gw.data?.items ?? []} pagination={{ pageSize: 30, hideOnSinglePage: true }}
                locale={{ emptyText: 'کاربری با پیامک دو مرحله‌ای پیدا نشد' }}
                columns={[
                  { title: 'نام کاربری', render: (_, u) => <Mono>{u.username}</Mono> },
                  { title: 'دستگاه', dataIndex: 'device_name' },
                  { title: 'موبایل', render: (_, u) => (u.mobile ? <Mono>{u.mobile}</Mono> : <Typography.Text type="danger">نامعتبر</Typography.Text>) },
                  { title: 'آخرین مشاهده', render: (_, u) => faDateTime(u.seen_at) },
                ]} />
            </Card>
          ),
        },
      ]} />
      <Modal open={editing !== null} title={editing?.id ? 'ویرایش کاربر' : 'کاربر جدید'} okText="ذخیره" cancelText="انصراف" onCancel={() => setEditing(null)} onOk={() => form.submit()}>
        {error && <Alert type="error" showIcon title={error} style={{ marginBottom: 12 }} />}
        <Form form={form} layout="vertical" requiredMark={false} onFinish={async (v) => {
          try {
            if (editing?.id) await api.put(`/otp/users/${editing.id}`, { ...v, password: v.password ?? '' })
            else await api.post('/otp/users', { ...v, password: v.password ?? '' })
            setEditing(null)
            reload()
          } catch (e) { setError((e as Error).message) }
        }}>
          <Form.Item name="username" label="نام کاربری (همان که در VPN وارد می‌شود)" rules={[{ required: true }]}><Input dir="ltr" /></Form.Item>
          <Form.Item name="name" label="نام"><Input /></Form.Item>
          <Form.Item name="mobile" label="موبایل" rules={[{ required: true }]}><Input dir="ltr" placeholder="0912xxxxxxx" /></Form.Item>
          <Form.Item name="source" label="بررسی رمز"><Radio.Group options={[{ value: 'local', label: 'محلی در TaktaPlus' }, { value: 'ldap', label: 'LDAP/AD' }]} /></Form.Item>
          {source === 'local' && (
            <Form.Item name="password" label="رمز" extra={editing?.id ? 'برای حفظ رمز فعلی خالی بگذارید' : 'حداقل ۱۲ کاراکتر'} rules={editing?.id ? [] : [{ required: true, min: 12 }]}>
              <Input.Password dir="ltr" autoComplete="new-password" />
            </Form.Item>
          )}
          <Form.Item name="fortinet_group" label="گروه Fortinet (Fortinet-Group-Name)"><Input dir="ltr" /></Form.Item>
          <Form.Item name="active" label="فعال" valuePropName="checked"><Switch /></Form.Item>
        </Form>
      </Modal>
      <ImportModal open={importing} onClose={(n) => { setImporting(false); if (n) { message.success(`${faNum(n)} کاربر وارد شد`); reload() } }} />
    </>
  )
}

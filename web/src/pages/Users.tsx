import { useState } from 'react'
import { Alert, Button, Card, Form, Input, Modal, Popconfirm, Select, Space, Switch, Table, Tag, App as AntApp } from 'antd'
import { api, type UserInfo } from '../api'
import { useAuth } from '../auth'
import { roleNames } from '../AppLayout'
import { faDateTime } from '../format'
import { Mono, PageHeader } from '../ui'
import { useFetch } from '../useFetch'

const roleOptions = Object.entries(roleNames).map(([value, label]) => ({ value, label }))

type Editing = { mode: 'new' } | { mode: 'edit'; user: UserInfo } | { mode: 'password'; user: UserInfo }

export default function Users() {
  const { message } = AntApp.useApp()
  const { user: me } = useAuth()
  const { data, loading, reload } = useFetch<{ items: UserInfo[] }>('/users')
  const [editing, setEditing] = useState<Editing | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [form] = Form.useForm()

  const act = async (fn: () => Promise<unknown>, ok: string) => {
    try {
      await fn()
      message.success(ok)
      void reload()
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const submit = async (v: Record<string, string | boolean>) => {
    setError(null)
    try {
      if (editing?.mode === 'new') await api.post('/users', v)
      else if (editing?.mode === 'edit') await api.put(`/users/${editing.user.id}`, v)
      else if (editing?.mode === 'password') await api.post(`/users/${editing.user.id}/password`, { password: v.password })
      setEditing(null)
      void reload()
    } catch (e) {
      setError((e as Error).message)
    }
  }

  return (
    <>
      <PageHeader title="کاربران پنل" subtitle="ورود دو مرحله‌ای (TOTP) برای همه‌ی کاربران الزامی است."
        extra={<Button type="primary" onClick={() => { setError(null); form.resetFields(); form.setFieldsValue({ role: 'operator' }); setEditing({ mode: 'new' }) }}>+ کاربر جدید</Button>} />
      <Card>
        <Table<UserInfo> rowKey="id" loading={loading} dataSource={data?.items ?? []} scroll={{ x: 900 }} pagination={false}
          columns={[
            { title: 'نام کاربری', render: (_, u) => <Mono>{u.username}</Mono> },
            { title: 'نام', dataIndex: 'name' },
            { title: 'نقش', render: (_, u) => roleNames[u.role] ?? u.role },
            {
              title: 'وضعیت', render: (_, u) => (
                <Space size={4}>
                  {u.active ? <Tag color="green">فعال</Tag> : <Tag>غیرفعال</Tag>}
                  {u.locked && <Tag color="red">قفل</Tag>}
                  {!u.totp_enabled && <Tag color="gold">2FA تنظیم نشده</Tag>}
                </Space>
              ),
            },
            { title: 'آخرین ورود', render: (_, u) => faDateTime(u.last_login_at) },
            {
              title: '', render: (_, u) => (
                <Space wrap size={0}>
                  <Button size="small" type="link" onClick={() => { setError(null); form.resetFields(); form.setFieldsValue(u); setEditing({ mode: 'edit', user: u }) }}>ویرایش</Button>
                  <Button size="small" type="link" onClick={() => { setError(null); form.resetFields(); setEditing({ mode: 'password', user: u }) }}>تغییر رمز</Button>
                  {u.totp_enabled && u.id !== me?.id && (
                    <Popconfirm title="کاربر باید در ورود بعدی Authenticator را دوباره تنظیم کند." okText="بازنشانی" cancelText="انصراف"
                      onConfirm={() => act(() => api.post(`/users/${u.id}/reset-mfa`), '2FA بازنشانی شد')}>
                      <Button size="small" type="link">بازنشانی 2FA</Button>
                    </Popconfirm>
                  )}
                  {u.locked && <Button size="small" type="link" onClick={() => void act(() => api.post(`/users/${u.id}/unlock`), 'قفل باز شد')}>باز کردن قفل</Button>}
                </Space>
              ),
            },
          ]} />
      </Card>
      <Modal open={editing !== null} destroyOnHidden okText="ذخیره" cancelText="انصراف" onCancel={() => setEditing(null)} onOk={() => form.submit()}
        title={editing?.mode === 'new' ? 'کاربر جدید' : editing?.mode === 'edit' ? `ویرایش ${editing.user.username}` : `تغییر رمز ${editing?.user.username ?? ''}`}>
        {error && <Alert type="error" showIcon title={error} style={{ marginBottom: 12 }} />}
        <Form form={form} layout="vertical" requiredMark={false} onFinish={submit} preserve={false}>
          {editing?.mode === 'new' && <Form.Item name="username" label="نام کاربری" rules={[{ required: true }]}><Input dir="ltr" autoComplete="off" /></Form.Item>}
          {editing?.mode !== 'password' && (
            <>
              <Form.Item name="name" label="نام" rules={[{ required: true }]}><Input /></Form.Item>
              <Form.Item name="email" label="ایمیل"><Input dir="ltr" /></Form.Item>
              <Form.Item name="role" label="نقش" rules={[{ required: true }]}><Select options={roleOptions} /></Form.Item>
            </>
          )}
          {editing?.mode === 'edit' && (
            <>
              <Form.Item name="phone" label="موبایل"><Input dir="ltr" /></Form.Item>
              <Form.Item name="active" label="فعال" valuePropName="checked"><Switch /></Form.Item>
            </>
          )}
          {editing?.mode !== 'edit' && (
            <Form.Item name="password" label="رمز عبور" extra="حداقل ۱۲ کاراکتر" rules={[{ required: true, min: 12, message: 'حداقل ۱۲ کاراکتر' }]}>
              <Input.Password dir="ltr" autoComplete="new-password" />
            </Form.Item>
          )}
        </Form>
      </Modal>
    </>
  )
}

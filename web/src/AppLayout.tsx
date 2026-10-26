import { useState } from 'react'
import { Avatar, Badge, Button, Drawer, Grid, Layout, Menu, Typography } from 'antd'
import { Link, Outlet, useLocation } from 'react-router'
import { useAuth, can } from './auth'
import { brand } from './theme'
import { useFetch } from './useFetch'
import type { LicenseInfo } from './api'
import { faNum } from './format'

const { Sider, Header, Content } = Layout

export const roleNames: Record<string, string> = {
  superadmin: 'مدیر ارشد', admin: 'مدیر', approver: 'تأییدکننده', operator: 'اپراتور', viewer: 'مشاهده‌گر',
}

function NavMenu({ onNavigate, openAlerts }: { onNavigate?: () => void; openAlerts: number }) {
  const { user } = useAuth()
  const { pathname } = useLocation()
  const key = '/' + (pathname.split('/')[1] ?? '')
  const link = (to: string, label: React.ReactNode) => ({ key: to, label: <Link to={to} onClick={onNavigate}>{label}</Link> })
  const system = [
    ...(can(user, 'users.manage') ? [link('/users', 'کاربران پنل')] : []),
    ...(can(user, 'license.read') ? [link('/license', 'لایسنس')] : []),
    ...(can(user, 'audit.read') ? [link('/audit', 'لاگ ممیزی')] : []),
  ]
  const items = [
    link('/', 'داشبورد'),
    {
      type: 'group' as const, label: 'پایش و نگهداری',
      children: [
        link('/devices', 'تجهیزات'),
        link('/alerts', <span>هشدارها {openAlerts > 0 && <Badge count={openAlerts} size="small" style={{ marginInlineStart: 6 }} />}</span>),
        link('/backups', 'بکاپ و ریستور'),
      ],
    },
    ...(system.length ? [{ type: 'group' as const, label: 'سیستم', children: system }] : []),
  ]
  return <Menu mode="inline" selectedKeys={[key]} items={items} style={{ borderInlineEnd: 0 }} />
}

function Brand() {
  return (
    <Link to="/" style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 4, padding: '20px 16px 12px' }}>
      <img src="/logo.png" alt="TaktaPlus" style={{ width: 150, height: 'auto' }} />
      <Typography.Text type="secondary" style={{ fontSize: 12 }}>مدیریت FortiGate و FortiWeb</Typography.Text>
    </Link>
  )
}

// LicenseFoot summarizes the installed license under the menu.
function LicenseFoot({ info }: { info: LicenseInfo | null }) {
  if (!info) return null
  const items = info.license?.items.filter((i) => i.kind === 'device') ?? []
  const active = items.filter((i) => i.status === 'active').length
  const grace = items.filter((i) => i.status === 'grace').length
  return (
    <Link to="/license" style={{ display: 'block', margin: 16, padding: 12, borderRadius: 8, background: brand.tint, color: 'inherit', fontSize: 12 }}>
      {info.license ? (
        <>
          <div style={{ fontWeight: 600, color: brand.primary }}>لایسنس: {faNum(active)} سریال فعال</div>
          {grace > 0 && <div style={{ color: '#D48806' }}>{faNum(grace)} سریال در مهلت</div>}
        </>
      ) : (
        <div style={{ fontWeight: 600, color: brand.primary }}>لایسنس نصب نشده است</div>
      )}
    </Link>
  )
}

export default function AppLayout() {
  const { user, logout } = useAuth()
  const screens = Grid.useBreakpoint()
  const [open, setOpen] = useState(false)
  const mobile = !screens.lg
  const lic = useFetch<LicenseInfo>(can(user, 'license.read') ? '/license' : null)
  const alerts = useFetch<{ open: Record<string, number> }>('/alerts?state=open&limit=1')
  const openAlerts = Object.values(alerts.data?.open ?? {}).reduce((a, b) => a + b, 0)

  return (
    <Layout style={{ minHeight: '100vh' }}>
      {!mobile && (
        <Sider width={240} style={{ borderInlineStart: '1px solid #EEE' }}>
          <Brand />
          <NavMenu openAlerts={openAlerts} />
          <LicenseFoot info={lic.data} />
        </Sider>
      )}
      <Drawer open={mobile && open} onClose={() => setOpen(false)} placement="right" size={260} styles={{ body: { padding: 0 } }}>
        <Brand />
        <NavMenu openAlerts={openAlerts} onNavigate={() => setOpen(false)} />
        <LicenseFoot info={lic.data} />
      </Drawer>
      <Layout>
        <Header style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12, paddingInline: 24, borderBottom: '1px solid #EEE' }}>
          {mobile ? <Button onClick={() => setOpen(true)}>منو</Button> : <span />}
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <Avatar style={{ background: brand.primary }}>{user?.name.slice(0, 1)}</Avatar>
            <div style={{ lineHeight: 1.4 }}>
              <div style={{ fontWeight: 600 }}>{user?.name}</div>
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>{roleNames[user?.role ?? ''] ?? user?.role}</Typography.Text>
            </div>
            <Button type="link" onClick={() => void logout()}>خروج</Button>
          </div>
        </Header>
        <Content style={{ padding: 24 }}>
          <Outlet />
        </Content>
      </Layout>
    </Layout>
  )
}

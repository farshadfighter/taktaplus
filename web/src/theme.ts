import type { ThemeConfig } from 'antd'

// Brand: TaktaPlus maroon (#900A3C) from the logo, with neutral grays.
export const brand = {
  primary: '#900A3C',
  primaryDark: '#6E0830',
  tint: '#FBEFF3',
  gray: '#A8A8A8',
}

export const theme: ThemeConfig = {
  token: {
    colorPrimary: brand.primary,
    colorLink: brand.primary,
    colorLinkHover: brand.primaryDark,
    borderRadius: 8,
    fontFamily: 'Vazirmatn, Tahoma, sans-serif',
    colorBgLayout: '#F5F5F6',
  },
  components: {
    Layout: { siderBg: '#FFFFFF', headerBg: '#FFFFFF' },
    Menu: { itemSelectedBg: brand.tint, itemSelectedColor: brand.primary },
  },
}

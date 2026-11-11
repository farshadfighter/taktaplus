// Jalali (Solar Hijri) date pickers: Ant Design pickers driven by a dayjs
// generate config whose dates use the jalali calendar. Values given to
// and returned from the pickers are ordinary dayjs instants, so callers
// keep using toISOString().

import { DatePicker as AntDatePicker, TimePicker as AntTimePicker } from 'antd'
import dayjs, { type Dayjs } from 'dayjs'
import 'dayjs/locale/fa'
import jalaliday from 'jalaliday/dayjs'
import base from '@rc-component/picker/es/generate/dayjs'
import type { GenerateConfig } from '@rc-component/picker/es/generate'

dayjs.extend(jalaliday)

const months = ['فروردین', 'اردیبهشت', 'خرداد', 'تیر', 'مرداد', 'شهریور', 'مهر', 'آبان', 'آذر', 'دی', 'بهمن', 'اسفند']
const weekDays = ['ی', 'د', 'س', 'چ', 'پ', 'ج', 'ش'] // Sunday first, as dayjs numbers them

const faDigits = (s: string) => s.replace(/\d/g, (d) => '۰۱۲۳۴۵۶۷۸۹'[Number(d)])
const enDigits = (s: string) => s.replace(/[۰-۹]/g, (d) => String('۰۱۲۳۴۵۶۷۸۹'.indexOf(d))).replace(/[٠-٩]/g, (d) => String('٠١٢٣٤٥٦٧٨٩'.indexOf(d)))

// J views any dayjs value in the jalali calendar.
const J = (d: Dayjs) => dayjs(d.valueOf()).calendar('jalali')

const jalaliConfig: GenerateConfig<Dayjs> = {
  ...base,
  getNow: () => dayjs().calendar('jalali'),
  getFixedDate: (s) => dayjs(s, ['YYYY-M-DD', 'YYYY-MM-DD']).calendar('jalali'),
  getEndDate: (d) => J(d).endOf('month'),
  getWeekDay: (d) => J(d).day(),
  getYear: (d) => J(d).year(),
  getMonth: (d) => J(d).month(),
  getDate: (d) => J(d).date(),
  addYear: (d, n) => J(d).add(n, 'year'),
  addMonth: (d, n) => J(d).add(n, 'month'),
  addDate: (d, n) => J(d).add(n, 'day'),
  setYear: (d, y) => J(d).year(y),
  setMonth: (d, m) => J(d).month(m),
  setDate: (d, n) => J(d).date(n),
  locale: {
    ...base.locale,
    getWeekFirstDay: () => 6, // Saturday
    getWeekFirstDate: (_, d) => {
      const x = J(d)
      return x.subtract((x.day() + 1) % 7, 'day')
    },
    getShortWeekDays: () => weekDays,
    getShortMonths: () => months,
    format: (_, d, format) => faDigits(J(d).format(format.replace('MMMM', '[' + months[J(d).month()] + ']').replace(/MMM(?!M)/, '[' + months[J(d).month()] + ']'))),
    parse: (_, text, formats) => {
      const t = enDigits(text.trim())
      for (const f of formats) {
        // Parse as gregorian digits, then read them as jalali fields.
        const g = dayjs(t, f, true)
        if (!g.isValid()) continue
        const j = dayjs().calendar('jalali').year(g.year()).month(g.month()).date(g.date()).hour(g.hour()).minute(g.minute()).second(g.second()).millisecond(0)
        if (j.isValid()) return j
      }
      return null
    },
  },
}

const generated = AntDatePicker.generatePicker<Dayjs>(jalaliConfig)

// DatePicker and RangePicker in the Jalali calendar.
export const JDatePicker = generated
export const JRangePicker = generated.RangePicker
export const TimePicker = AntTimePicker

/** Backend limits Unicode code points after trimming, rather than UTF-16 units. */
export const noteTooLong = (value: string) => Array.from(value.trim()).length > 500
export const displayNote = (value?: string | null) => value?.trim() ? value : '未填写备注'

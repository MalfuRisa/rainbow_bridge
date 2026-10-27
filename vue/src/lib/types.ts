export interface Environment {
  environmentId: string
  id: string
  key: string
  name: string
  pipelines: Pipeline[]
}

export interface Pipeline {
  environmentId: string
  scopeId: string
  id: string
  key: string
  name: string
}

export type ConfigType = 
  | 'text'
  | 'textarea'
  | 'richtext'
  | 'number'
  | 'decimal'
  | 'boolean'
  | 'keyvalue'
  | 'object'
  | 'color'
  | 'file'
  | 'image'

export const CONFIG_TYPE_META: Record<ConfigType, { label: string; color: string; description: string }> = {
  text: { 
    label: "文本", 
    color: "bg-primary/10 text-primary dark:bg-primary/20 dark:text-primary/80",
    description: "单行文本输入"
  },
  textarea: { 
    label: "多行文本", 
    color: "bg-secondary/10 text-secondary dark:bg-secondary/20 dark:text-secondary/80",
    description: "多行文本输入"
  },
  richtext: { 
    label: "富文本", 
    color: "bg-accent/10 text-accent dark:bg-accent/20 dark:text-accent/80",
    description: "支持HTML格式的富文本"
  },
  number: { 
    label: "整数", 
    color: "bg-amber-100 text-amber-800 dark:bg-amber-900/20 dark:text-amber-400",
    description: "整数数值"
  },
  decimal: { 
    label: "小数", 
    color: "bg-orange-100 text-orange-800 dark:bg-orange-900/20 dark:text-orange-400",
    description: "浮点数数值"
  },
  boolean: { 
    label: "布尔值", 
    color: "bg-accent/10 text-accent dark:bg-accent/20 dark:text-accent/80",
    description: "真/假开关"
  },
  keyvalue: { 
    label: "键值对", 
    color: "bg-secondary/10 text-secondary dark:bg-secondary/20 dark:text-secondary/80",
    description: "键值对列表"
  },
  object: { 
    label: "对象", 
    color: "bg-primary/10 text-primary dark:bg-primary/20 dark:text-primary/80",
    description: "JSON对象"
  },
  color: { 
    label: "色彩", 
    color: "bg-accent/10 text-accent dark:bg-accent/20 dark:text-accent/80",
    description: "颜色选择器"
  },
  file: { 
    label: "文件", 
    color: "bg-blue-100 text-blue-800 dark:bg-blue-900/20 dark:text-blue-400",
    description: "文件上传"
  },
  image: { 
    label: "图片", 
    color: "bg-emerald-100 text-emerald-800 dark:bg-emerald-900/20 dark:text-emerald-400",
    description: "图片上传"
  },
}

export interface ConfigItem {
  scopeId: string
  editRevision?: string
  id: string
  name: string
  alias: string
  type: ConfigType
  content: string
  environmentId: string
  pipelineId: string
  description: string
  createdAt: string
  updatedAt: string
}

export interface RuntimeOverview {
  environments: Environment[]
}

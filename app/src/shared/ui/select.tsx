import { Check, ChevronDown, type LucideIcon } from 'lucide-react';

interface SelectProps {
  label: string;
  value: string;
  options: { value: string; label: string }[];
  onChange: (value: string) => void;
  filterIcon?: LucideIcon;
}

export function Select({ label, value, options, onChange, filterIcon: Icon }: SelectProps) {
  const selected = options.find((option) => option.value === value);
  return (
    <select
      className={Icon ? 'journal-select filter-trigger' : 'journal-select'}
      aria-label={label}
      data-active={Icon ? Boolean(value) : undefined}
      value={value}
      onChange={(event) => onChange(event.target.value)}
    >
      <button type="button">
        {Icon && <Icon size={16} aria-hidden="true" />}
        <span>{Icon && !value ? label : selected?.label}</span>
        <ChevronDown className="select-chevron" size={16} aria-hidden="true" />
      </button>
      {options.map((option) => (
        <option key={option.value} value={option.value}>
          {option.label}
          <Check className="option-check" size={16} aria-hidden="true" />
        </option>
      ))}
    </select>
  );
}

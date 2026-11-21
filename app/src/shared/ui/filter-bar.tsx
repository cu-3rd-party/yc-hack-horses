import { useEffect, useState, type ReactNode } from 'react';

interface FilterBarProps<T> {
  value: T;
  onChange: (value: T) => void;
  children: (draft: T, edit: (value: T) => void) => ReactNode;
}

export function FilterBar<T>({ value, onChange, children }: FilterBarProps<T>) {
  const [draft, edit] = useState(value);
  useEffect(() => {
    if (draft === value) return;
    const timer = window.setTimeout(() => onChange(draft), 300);
    return () => window.clearTimeout(timer);
  }, [draft, value, onChange]);

  return <div className="filters">{children(draft, edit)}</div>;
}

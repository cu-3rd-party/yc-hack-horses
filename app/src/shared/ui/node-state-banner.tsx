import { Badge } from './badge';
import { cn } from '../lib/utils';
import { isNodeUnavailable } from '../api/hooks';

interface Props {
  loading: boolean;
  error: Error | null;
  className?: string;
}

// Единый баннер состояния ноды: недоступна/загрузка/ошибка.
export function NodeStateBanner({ loading, error, className }: Props) {
  if (loading) {
    return <Badge variant="muted" className={cn(className)}>Загрузка...</Badge>;
  }
  if (!error) return null;
  if (isNodeUnavailable(error)) {
    return <Badge variant="warning" className={cn(className)}>Нода недоступна (offline)</Badge>;
  }
  return <Badge variant="error" className={cn(className)}>{error.message}</Badge>;
}

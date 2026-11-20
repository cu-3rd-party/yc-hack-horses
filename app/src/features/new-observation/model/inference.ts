// Локальный inference-стаб: если модель доступна на ноде — используем её
// результат, иначе сразу ручной ввод признаков (ADR-0012).
export interface InferenceResult {
  available: boolean;
  model_version: string;
  traits: { trait: string; value: string; confidence?: number | null }[];
  confidence: number;
  fingerprint?: string | null;
}

export async function runLocalInference(file: File): Promise<InferenceResult> {
  // V0: inference-эндпоинт на ноде ещё не стабилизирован — считаем
  // недоступным и переходим к ручному вводу. Точка расширения.
  void file;
  return { available: false, model_version: 'manual-v0', traits: [], confidence: 0 };
}

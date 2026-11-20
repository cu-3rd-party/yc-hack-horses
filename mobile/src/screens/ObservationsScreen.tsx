import React, { useCallback, useEffect, useState } from 'react';
import { FlatList, StyleSheet, Text, View } from 'react-native';
import { getObservations } from '../shared/api/endpoints';
import { goCore } from '../shared/go-core/bridge';
import type { Observation } from '@yc-hack-horses/contracts';

// Список наблюдений. Источник: REST ноды; без сети — пустой список с подсказкой
// (локальный кеш-проекций в V0 не строится, события читает Go Core).
export function ObservationsScreen() {
  const [items, setItems] = useState<Observation[]>([]);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    getObservations()
      .then((r) => setItems(r.observations ?? []))
      .catch((err: unknown) =>
        setError(err instanceof Error ? err.message : String(err)),
      );
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  return (
    <View style={styles.container}>
      {error ? <Text style={styles.error}>{error}</Text> : null}
      {items.length === 0 && !error ? (
        <Text style={styles.muted}>Наблюдений нет или нода недоступна</Text>
      ) : null}
      <FlatList
        data={items}
        keyExtractor={(o) => o.id}
        renderItem={({ item }) => (
          <View style={styles.row}>
            <Text style={styles.id}>{item.id.slice(0, 8)}…</Text>
            <Text style={styles.muted}>
              {item.observed_at} · {item.horse_id ? item.horse_id.slice(0, 8) + '…' : 'без лошади'} ·
              фото: {item.blob_ids.length}
            </Text>
          </View>
        )}
      />
      {!goCore.available ? (
        <Text style={styles.muted}>Офлайн-наблюдения лежат в очереди; см. экран «Синхронизация»</Text>
      ) : null}
    </View>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, padding: 16, gap: 8 },
  row: { paddingVertical: 8, borderBottomWidth: StyleSheet.hairlineWidth, borderColor: '#ddd', gap: 2 },
  id: { fontWeight: '600' },
  muted: { color: '#666' },
  error: { color: '#c0392b' },
});

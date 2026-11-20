import React, { useEffect, useState } from 'react';
import { ScrollView, StyleSheet, Text, View } from 'react-native';
import { Button } from '../ui/Button';
import { goCore } from '../shared/go-core/bridge';
import { getNodeStatus } from '../shared/api/endpoints';
import type { NodeStatus } from '@yc-hack-horses/contracts';

// Dashboard: статус ноды (Go Core или REST) + навигация по разделам.
export function DashboardScreen({ navigation }: { navigation: { navigate: (s: string) => void } }) {
  const [status, setStatus] = useState<NodeStatus | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    getNodeStatus()
      .then((s) => alive && setStatus(s))
      .catch((err: unknown) => alive && setError(err instanceof Error ? err.message : String(err)));
    return () => {
      alive = false;
    };
  }, []);

  return (
    <ScrollView contentContainerStyle={styles.container}>
      <Text style={styles.h1}>Мониторинг лошадей</Text>
      {status ? (
        <View>
          <Text>Нода: {status.node_id}</Text>
          <Text>Роль: {status.role}</Text>
          {status.storage ? <Text>Событий в очереди синка: {status.storage.pending_events ?? 0}</Text> : null}
        </View>
      ) : (
        <Text style={styles.muted}>{error ?? 'Статус ноды недоступен'}</Text>
      )}
      <View style={styles.buttons}>
        <Button title="Наблюдения" onPress={() => navigation.navigate('Observations')} />
        <Button title="Новое наблюдение" onPress={() => navigation.navigate('NewObservation')} />
        <Button title="Синхронизация" onPress={() => navigation.navigate('Sync')} />
      </View>
      <Text style={styles.muted}>
        {goCore.available ? 'Go Core: активен' : 'Go Core: отсутствует в сборке'}
      </Text>
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  container: { padding: 16, gap: 16 },
  h1: { fontSize: 22, fontWeight: '700' },
  muted: { color: '#666' },
  buttons: { gap: 8, marginTop: 8 },
});

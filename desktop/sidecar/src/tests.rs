#[cfg(test)]
mod tests {
    use crate::{health_ok, Sidecar};
    use super::*;

    #[test]
    fn health_refuses_closed_port() {
        // Порт в диапазоне эфемерных, но никто не слушает — соединение должно
        // завершиться ошибкой быстро.
        assert!(!health_ok(1));
    }

    #[test]
    fn sidecar_defaults_from_env() {
        // Не задаём YC_NODE_PORT — порт по умолчанию 8080.
        std::env::remove_var("YC_NODE_PORT");
        let s = Sidecar::new();
        let st = s.status();
        assert_eq!(st.port, 8080);
        assert!(!st.running);
        assert_eq!(st.restarts, 0);
    }

    #[test]
    fn shutdown_without_start_is_noop() {
        std::env::remove_var("YC_NODE_PORT");
        let mut s = Sidecar::new();
        s.shutdown();
        assert!(!s.status().running);
    }
}

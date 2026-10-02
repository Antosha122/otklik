// Command vapidkeygen печатает пару VAPID-ключей для Web Push.
// Скопируйте вывод в .env (VAPID_PUBLIC_KEY / VAPID_PRIVATE_KEY).
// ВАЖНО: пара генерируется один раз и дальше не меняется — смена ключей
// отзывает все подписки браузеров (приложение запросит их заново).
package main

import (
	"fmt"
	"os"

	"otklik/internal/push"
)

func main() {
	pub, priv, err := push.GenerateKeyPair()
	if err != nil {
		fmt.Fprintln(os.Stderr, "vapidkeygen:", err)
		os.Exit(1)
	}
	fmt.Println("VAPID_PUBLIC_KEY=" + pub)
	fmt.Println("VAPID_PRIVATE_KEY=" + priv)
}

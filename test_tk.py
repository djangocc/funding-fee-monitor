import tkinter as tk
import threading
import time

root = tk.Tk()
root.title("Test")
root.configure(bg="#1e1e1e")
root.attributes("-topmost", True)
root.attributes("-alpha", 0.85)
root.overrideredirect(True)
root.geometry("300x100")

label = tk.Label(root, text="waiting...", font=("Menlo", 14), bg="#1e1e1e", fg="#ffffff")
label.pack(expand=True)

counter = [0]

def update_label():
    counter[0] += 1
    label.config(text=f"Update #{counter[0]}")
    print(f"Updated to #{counter[0]}", flush=True)
    root.after(1000, update_label)

# Method 1: direct root.after
root.after(500, update_label)

root.mainloop()

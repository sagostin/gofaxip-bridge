# uid2gateway.py
import csv
import sys

def find_gateway(phone_number, csv_file='/opt/gofaxip-process/gateway_phone.csv'):
    try:
        with open(csv_file, newline='') as file:
            reader = csv.reader(file)
            for row in reader:
                if row[1] == phone_number:
                    return row[0]
    except Exception as e:
        print(f"Error reading CSV file: {e}", file=sys.stderr)
    return None

def main():
    if len(sys.argv) != 2:
        print("Usage: uid2gateway.py <phone_number>", file=sys.stderr)
        sys.exit(1)

    phone_number = sys.argv[1]
    gateway = find_gateway(phone_number)
    if gateway:
        print(gateway)

if __name__ == "__main__":
    main()
